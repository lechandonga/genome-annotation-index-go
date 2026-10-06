// Package store provides the persistent annotation index: atomic batch
// imports, versioned history, rollback, interval/strand queries and
// crash-safe recovery.
//
// # Concurrency model
//
// Readers never take locks: the committed Snapshot is published through an
// atomic.Value, so a query observes either the complete pre-rebuild view or
// the complete post-rebuild view, never a mixture. Rebuild swaps the pointer
// once, after a full new snapshot has been validated.
//
// Durability layout (<dir>)
//
//   - events.jsonl : append-only, checksummed commit/rollback history;
//   - snapshot.json: materialized snapshot accelerator (re-derived on bad);
//   - manifest.json: atomically renamed pointer to the materialized state;
//   - sources/     : optional copies of imported files for reproducibility;
//   - lock         : process-exclusive flock while the store is open.
//
// Recovery semantics on open
//
//   - truncated final event (crash mid-append) -> tail discarded safely;
//   - checksum failure inside the log          -> CorruptError (explicit
//     failure; callers may choose Purge then re-import);
//   - missing/bad snapshot or manifest         -> rebuilt from event log;
//   - empty directory                          -> fresh empty store.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

// CorruptError means on-disk state is inconsistent and cannot be used.
type CorruptError struct{ Detail string }

func (e *CorruptError) Error() string { return "index is corrupt: " + e.Detail }

// IsCorrupt reports whether err is a *CorruptError.
func IsCorrupt(err error) bool {
	var c *CorruptError
	return errors.As(err, &c)
}

// Store is an open persistent index.
type Store struct {
	dir  string
	lock *fileLock

	writeMu sync.Mutex
	catalog *graph.Catalog
	view    atomic.Pointer[graph.Snapshot]

	sources bool
}

// OpenOptions controls store opening.
type OpenOptions struct {
	// CopySources stores imported files under <dir>/sources.
	CopySources bool
}

// Open opens (or creates) a persistent index rooted at dir.
func Open(dir string, opts OpenOptions) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "sources"), 0o755); err != nil {
		return nil, err
	}
	lock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}

	st := &Store{dir: dir, lock: lock, sources: opts.CopySources}
	st.catalog = graph.NewCatalog()

	if err := st.recover(); err != nil {
		_ = lock.release()
		return nil, err
	}
	return st, nil
}

// Close releases the process lock.
func (s *Store) Close() error { return s.lock.release() }

// Dir returns the index directory.
func (s *Store) Dir() string { return s.dir }

// Revision returns the committed view revision.
func (s *Store) Revision() graph.Revision { return s.view.Load().Revision }

// Query evaluates q against the current committed snapshot.
func (s *Store) Query(q Query) []model.FeatureRef {
	return querySnapshot(s.view.Load(), q)
}

// Snapshot returns the current committed snapshot (read-only).
func (s *Store) Snapshot() *graph.Snapshot { return s.view.Load() }

func (s *Store) publish(snap *graph.Snapshot) { s.view.Store(snap) }

func sortStrings(in []string) { sort.Strings(in) }

func copySourceFile(srcPath, dir, sourceName string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	name := sourceName
	if name == "" {
		name = filepath.Base(srcPath)
	}
	return os.WriteFile(filepath.Join(dir, "sources", filepath.Base(name)+".src"), data, 0o644)
}

func (s *Store) recover() error {
	events, truncated, err := LoadEvents(s.dir)
	if err != nil {
		return &CorruptError{Detail: err.Error()}
	}
	if truncated {
		if err := truncateEvents(s.dir, len(events)); err != nil {
			return &CorruptError{Detail: "cannot drop truncated tail: " + err.Error()}
		}
	}

	cat, err := buildCatalogFromEvents(events)
	if err != nil {
		return &CorruptError{Detail: err.Error()}
	}
	s.catalog = cat
	s.publish(cat.Snapshot())

	if err := s.verifyMaterialized(); err != nil {
		if err := s.materializeLockedWith(cat.Snapshot()); err != nil {
			return &CorruptError{Detail: "rebuilding materialized snapshot: " + err.Error()}
		}
	}
	return nil
}

// Replay applies events in order, honoring rollback records by rebuilding the
// catalog from the pre-rollback prefix capped at the target revision.
func buildCatalogFromEvents(events []Event) (*graph.Catalog, error) {
	cat := graph.NewCatalog()
	for i := 0; i < len(events); i++ {
		ev := events[i]
		switch ev.Type {
		case EventCommit:
			conflict := graph.ConflictReject
			if ev.OnConflict == "overwrite" {
				conflict = graph.ConflictOverwrite
			}
			if _, probs := cat.Apply(ev.Features, graph.ApplyOptions{OnConflict: conflict}); probs != nil {
				return nil, fmt.Errorf("replay validation failed at revision %d: %v", ev.Revision, probs)
			}
		case EventRollback:
			prefix := commitsBefore(events[:i], ev.ToRevision)
			c, err := buildCatalogFromEvents(prefix)
			if err != nil {
				return nil, err
			}
			cat = c
		default:
			return nil, fmt.Errorf("unknown event type %q", ev.Type)
		}
	}
	return cat, nil
}

// commitsBefore keeps only commit events with revision <= target, dropping
// any rollback noise in the prefix.
func commitsBefore(prefix []Event, target graph.Revision) []Event {
	out := make([]Event, 0, len(prefix))
	for _, e := range prefix {
		if e.Type == EventCommit && e.Revision <= target {
			out = append(out, e)
		}
	}
	return out
}

// CommitResult describes a successful commit.
type CommitResult struct {
	Revision graph.Revision
	Source   string
	Count    int
}

// CommitOptions controls a commit.
type CommitOptions struct {
	Source     string
	OnConflict graph.OnConflict
	// SourcePath, when set and CopySources was enabled, is copied into the
	// store for later reproducible rebuild.
	SourcePath string
}

// Commit atomically validates, persists and publishes one batch. On any
// validation error nothing is persisted and the live view is unchanged.
func (s *Store) Commit(features []model.Feature, opts CommitOptions) (*CommitResult, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	snap, probs := s.catalog.Apply(features, graph.ApplyOptions{OnConflict: opts.OnConflict})
	if probs != nil {
		return nil, probs
	}

	if s.sources && opts.SourcePath != "" {
		if err := copySourceFile(opts.SourcePath, s.dir, opts.Source); err != nil {
			return nil, err
		}
	}

	conflictName := "reject"
	if opts.OnConflict == graph.ConflictOverwrite {
		conflictName = "overwrite"
	}
	ev := Event{
		Type:       EventCommit,
		Revision:   snap.Revision,
		Source:     opts.Source,
		OnConflict: conflictName,
		Features:   features,
	}
	if err := appendEvent(s.dir, &ev); err != nil {
		return nil, err
	}
	if err := s.materializeLockedWith(snap); err != nil {
		return nil, err
	}
	s.publish(snap)
	return &CommitResult{Revision: snap.Revision, Source: opts.Source, Count: len(features)}, nil
}

// Rollback moves the live view to an earlier revision. The rollback itself is
// durably recorded so reopening reproduces the same view; subsequent commits
// build on the rolled-back state.
func (s *Store) Rollback(to graph.Revision) (*graph.Snapshot, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cur := s.catalog.Revision()
	if to < 0 || to > cur {
		return nil, fmt.Errorf("rollback target %d outside [0,%d]", to, cur)
	}

	events, truncated, err := LoadEvents(s.dir)
	if err != nil {
		return nil, &CorruptError{Detail: err.Error()}
	}
	if truncated {
		return nil, &CorruptError{Detail: "unexpected truncated event tail during rollback"}
	}
	prefix := commitsBefore(events, to)
	cat, err := buildCatalogFromEvents(prefix)
	if err != nil {
		return nil, &CorruptError{Detail: err.Error()}
	}

	ev := Event{Type: EventRollback, Revision: cur, ToRevision: to}
	if err := appendEvent(s.dir, &ev); err != nil {
		return nil, err
	}
	s.catalog = cat
	snap := cat.Snapshot()
	if err := s.materializeLockedWith(snap); err != nil {
		return nil, err
	}
	s.publish(snap)
	return snap, nil
}

// Rebuild reconstructs the current view purely from the durable event log and
// swaps it in atomically. Concurrent queries either answer from the old
// snapshot or the rebuilt one.
func (s *Store) Rebuild() (graph.Revision, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	events, truncated, err := LoadEvents(s.dir)
	if err != nil {
		return 0, &CorruptError{Detail: err.Error()}
	}
	if truncated {
		if err := truncateEvents(s.dir, len(events)); err != nil {
			return 0, &CorruptError{Detail: err.Error()}
		}
	}
	cat, err := buildCatalogFromEvents(events)
	if err != nil {
		return 0, &CorruptError{Detail: err.Error()}
	}
	if probs := cat.Replace(cat.Snapshot()); probs != nil {
		return 0, probs
	}
	if err := s.materializeLockedWith(cat.Snapshot()); err != nil {
		return 0, err
	}
	s.catalog = cat
	s.publish(cat.Snapshot())
	return cat.Revision(), nil
}

// Purge removes all durable state (used after explicit operator decision
// following a CorruptError). The directory itself is preserved and reopened
// empty.
func (s *Store) Purge() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, p := range []string{eventsPath(s.dir), filepath.Join(s.dir, snapshotName), manifestPath(s.dir)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.catalog = graph.NewCatalog()
	s.publish(s.catalog.Snapshot())
	return s.materializeLockedWith(s.catalog.Snapshot())
}
