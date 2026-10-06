package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const snapshotName = "snapshot.json"

// Manifest is the top-of-store pointer. It is atomically renamed into place.
type Manifest struct {
	Revision   int64  `json:"revision"`
	EventCount int    `json:"event_count"`
	Snapshot   string `json:"snapshot"`
	SHA256     string `json:"sha256"`
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var errCorruptSnapshot = fmt.Errorf("snapshot file failed integrity check")
