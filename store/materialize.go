package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

type persistedFeature struct {
	Feature model.Feature `json:"feature"`
	Current bool          `json:"current"`
}

type persistedSnapshot struct {
	Revision int64                         `json:"revision"`
	Features map[string][]persistedFeature `json:"features"`
}

func (s *Store) materializeLockedWith(snap *graph.Snapshot) error {
	p := persistedSnapshot{
		Revision: int64(snap.Revision),
		Features: map[string][]persistedFeature{},
	}
	ids := make([]string, 0, len(snap.Features))
	for id := range snap.Features {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		for _, v := range snap.Features[id] {
			p.Features[id] = append(p.Features[id], persistedFeature{
				Feature: v.Feature, Current: v.Current,
			})
		}
	}
	body, err := json.MarshalIndent(&p, "", "  ")
	if err != nil {
		return err
	}
	snapFile := filepath.Join(s.dir, snapshotName)
	if err := writeFileAtomic(snapFile, body); err != nil {
		return err
	}
	sum, err := hashFile(snapFile)
	if err != nil {
		return err
	}
	events, _, err := LoadEvents(s.dir)
	if err != nil {
		return err
	}
	m := Manifest{
		Revision:   int64(snap.Revision),
		EventCount: len(events),
		Snapshot:   snapshotName,
		SHA256:     sum,
	}
	mb, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(manifestPath(s.dir), mb)
}

// verifyMaterialized validates that snapshot.json matches the manifest hash
// and describes the same revision/event count as the replayed state. Any
// mismatch returns an error that triggers safe regeneration.
func (s *Store) verifyMaterialized() error {
	mb, err := os.ReadFile(manifestPath(s.dir))
	if err != nil {
		return errCorruptSnapshot
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return errCorruptSnapshot
	}
	sum, err := hashFile(filepath.Join(s.dir, m.Snapshot))
	if err != nil {
		return errCorruptSnapshot
	}
	if sum != m.SHA256 {
		return errCorruptSnapshot
	}
	body, err := os.ReadFile(filepath.Join(s.dir, m.Snapshot))
	if err != nil {
		return errCorruptSnapshot
	}
	var p persistedSnapshot
	if err := json.Unmarshal(body, &p); err != nil {
		return errCorruptSnapshot
	}
	if p.Revision != int64(s.catalog.Revision()) {
		return errCorruptSnapshot
	}
	events, _, err := LoadEvents(s.dir)
	if err != nil {
		return err
	}
	if m.EventCount != len(events) {
		return errCorruptSnapshot
	}
	return nil
}
