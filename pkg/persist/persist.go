// Package persist implements crash-safe snapshot persistence.
//
// On-disk layout inside the index directory:
//
//	snapshot.json        canonical, checksummed view of the database
//	snapshot.json.sha256 hex SHA-256 of snapshot.json
//
// Writes go to a temporary file in the same directory, are fsynced and then
// atomically renamed. A crash therefore leaves either the previous complete
// snapshot or the new complete snapshot - never a mixed/truncated one.
// On open, checksum and structural integrity are verified; failure is
// reported as RejectSnapshot rather than returning possibly wrong results.
package persist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/registry"
)

const (
	snapshotFile = "snapshot.json"
	checksumFile = "snapshot.json.sha256"
	tempSuffix   = ".tmp"
)

// Paths returns the on-disk file locations under dir.
func Paths(dir string) (snapshot, checksum string) {
	return filepath.Join(dir, snapshotFile), filepath.Join(dir, checksumFile)
}

// Save atomically writes the snapshot and its checksum.
func Save(dir string, s *registry.Snapshot) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "mkdir: " + err.Error()}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "marshal: " + err.Error()}
	}
	sum := sha256.Sum256(data)

	snapPath, sumPath := Paths(dir)
	tmpSnap := snapPath + tempSuffix
	tmpSum := sumPath + tempSuffix
	if err := os.WriteFile(tmpSnap, data, 0o644); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "write temp: " + err.Error()}
	}
	if err := syncFile(tmpSnap); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "fsync: " + err.Error()}
	}
	checksumLine := hex.EncodeToString(sum[:]) + "  " + snapshotFile + "\n"
	if err := os.WriteFile(tmpSum, []byte(checksumLine), 0o644); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "write checksum: " + err.Error()}
	}
	if err := syncFile(tmpSum); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "fsync checksum: " + err.Error()}
	}
	if err := os.Rename(tmpSnap, snapPath); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "rename snapshot: " + err.Error()}
	}
	if err := os.Rename(tmpSum, sumPath); err != nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "rename checksum: " + err.Error()}
	}
	if dirF, err := os.Open(dir); err == nil {
		_ = dirF.Sync()
		_ = dirF.Close()
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Exists reports whether a snapshot file is present.
func Exists(dir string) bool {
	snapPath, _ := Paths(dir)
	_, err := os.Stat(snapPath)
	return err == nil
}

// Load reads and verifies the snapshot. Missing snapshot yields
// (nil, nil); checksum or structural failure yields RejectSnapshot.
func Load(dir string) (*registry.Snapshot, error) {
	snapPath, sumPath := Paths(dir)
	data, err := os.ReadFile(snapPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &model.RejectError{Kind: model.RejectSnapshot, Detail: "read snapshot: " + err.Error()}
	}
	want, err := os.ReadFile(sumPath)
	if err != nil {
		return nil, &model.RejectError{Kind: model.RejectSnapshot, Detail: "read checksum: " + err.Error()}
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	wantHex := strings.Fields(string(want))
	if len(wantHex) == 0 || wantHex[0] != got {
		return nil, &model.RejectError{Kind: model.RejectSnapshot,
			Detail: fmt.Sprintf("checksum mismatch: snapshot.json sha256=%s, recorded %q", got, strings.TrimSpace(string(want)))}
	}
	var s registry.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, &model.RejectError{Kind: model.RejectSnapshot, Detail: "invalid JSON: " + err.Error()}
	}
	if err := Verify(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Verify performs structural self-checks on a loaded snapshot.
func Verify(s *registry.Snapshot) error {
	if s == nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "empty snapshot"}
	}
	if s.Versions == nil || s.Current == nil || s.Sequences == nil {
		return &model.RejectError{Kind: model.RejectSnapshot, Detail: "snapshot maps are uninitialized"}
	}
	for id, hist := range s.Versions {
		if id == "" || len(hist) == 0 {
			return &model.RejectError{Kind: model.RejectSnapshot, Detail: "empty version history: " + id}
		}
		cur, ok := s.Current[id]
		if !ok || cur < 0 || cur >= len(hist) {
			return &model.RejectError{Kind: model.RejectSnapshot, Detail: "current pointer out of range: " + id}
		}
		prev := 0
		for i, f := range hist {
			if f.ID != id {
				return &model.RejectError{Kind: model.RejectSnapshot, Detail: "history id mismatch for " + id}
			}
			if i > 0 && f.Version <= prev {
				return &model.RejectError{Kind: model.RejectSnapshot, Detail: "non-monotonic versions for " + id}
			}
			prev = f.Version
		}
	}
	for id := range s.Current {
		if _, ok := s.Versions[id]; !ok {
			return &model.RejectError{Kind: model.RejectSnapshot, Detail: "current points at missing history: " + id}
		}
	}
	for id := range s.Current {
		f, _ := s.CurrentFeature(id)
		for _, p := range f.Parents {
			if _, ok := s.Current[p]; !ok {
				return &model.RejectError{Kind: model.RejectSnapshot, Detail: "dangling parent " + p + " <- " + id}
			}
		}
		if _, ok := s.Sequences[f.SeqID]; !ok {
			return &model.RejectError{Kind: model.RejectSnapshot, Detail: "unknown sequence " + f.SeqID + " on " + id}
		}
	}
	// Snapshot content checksum cross-check is implicit in the file hash.
	return nil
}

// Discard removes a broken snapshot so a safe rebuild can start fresh.
func Discard(dir string) error {
	snapPath, sumPath := Paths(dir)
	for _, p := range []string{snapPath, sumPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
