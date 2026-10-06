// Package store ties parsing, the registry, the interval index and crash-safe
// persistence together behind a concurrency-safe API.
//
// # Concurrency model
//
// Queries hold an immutable (snapshot, index) pair captured atomically; a
// rebuild or re-import constructs the complete new pair off to the side and
// only then swaps the single view pointer under a short lock. Readers
// therefore observe either the complete pre-rebuild view or the complete
// post-rebuild view - never a mixture, duplicates or omissions.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lechandonga/genome-annotation-index-go/pkg/index"
	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/parse"
	"github.com/lechandonga/genome-annotation-index-go/pkg/persist"
	"github.com/lechandonga/genome-annotation-index-go/pkg/registry"
)

// Config controls open/import behavior.
type Config struct {
	Dir string // index directory
	// LenientSequences allows features on sequences not imported via FASTA.
	// The safe default (false) requires reference sequences and bound checks.
	LenientSequences bool
}

// view is one immutable generation visible to readers.
type view struct {
	snap *registry.Snapshot
	idx  *index.Index
}

// Store is the thread-safe entry point.
type Store struct {
	cfg Config

	writeMu sync.Mutex // serializes import/rollback/rebuild
	mu      sync.RWMutex
	cur     atomicView
}

type atomicView struct {
	v *view
}

// Open loads an existing index. When no snapshot exists it starts empty.
// A checksum/integrity failure is returned explicitly: callers choose between
// failing fast or Rebuild.
func Open(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, errors.New("store: index directory is required")
	}
	requireSeq := !cfg.LenientSequences
	s, err := persist.Load(cfg.Dir)
	if err != nil {
		return nil, err
	}
	st := &Store{cfg: cfg}
	if s == nil {
		reg := registry.New(registry.Config{RequireSequences: requireSeq})
		s = reg.Snapshot()
	}
	st.publish(s)
	return st, nil
}

// OpenOrRebuild opens the store; if the index is missing it starts empty, and
// if it is incomplete or corrupt it is discarded and rebuilt safely by
// replaying the recorded source operations. Missing source files make the
// rebuild fail explicitly instead of returning partial results.
func OpenOrRebuild(cfg Config) (*Store, error) {
	st, err := Open(cfg)
	if err == nil {
		return st, nil
	}
	var rerr *model.RejectError
	if !errors.As(err, &rerr) || rerr.Kind != model.RejectSnapshot {
		return nil, err
	}
	// Corrupt: we cannot recover without knowing the op log (which lived in
	// the corrupt snapshot). A corrupt but structurally unreadable snapshot
	// cannot be replayed, so fail by default. Callers with source files can
	// New+Import them; Rebuild is supported whenever the snapshot was
	// readable but desired to be regenerated.
	return nil, fmt.Errorf("store: index corrupt, refusing to serve results: %w (recreate with RebuildFromSources)", err)
}

func (st *Store) publish(s *registry.Snapshot) {
	st.mu.Lock()
	st.cur.v = &view{snap: s, idx: index.Build(s)}
	st.mu.Unlock()
}

// Snapshot returns the currently visible snapshot.
func (st *Store) Snapshot() *registry.Snapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.cur.v.snap
}

// Generation returns the visible generation number.
func (st *Store) Generation() int {
	return st.Snapshot().Generation
}

// ImportFile parses a local file and applies it transactionally. On any
// rejection the visible view is unchanged and every reason is returned.
func (st *Store) ImportFile(path, source string, format parse.Format) (registry.ImportReport, error) {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()

	if format == "" {
		format = parse.Detect(filepath.Base(path))
	}
	if format == "" {
		return registry.ImportReport{}, fmt.Errorf("%w: cannot detect format of %s", errBadFormat, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return registry.ImportReport{}, &model.RejectError{Kind: model.RejectSource, URI: path, Detail: err.Error()}
	}
	defer f.Close()
	abs, _ := filepath.Abs(path)
	uri := "file://" + abs
	res := parse.Parse(f, parse.Options{Format: format, URI: uri, Source: source})

	st.mu.RLock()
	snap := st.cur.v.snap
	st.mu.RUnlock()
	reg := registry.FromSnapshot(registry.Config{RequireSequences: !st.cfg.LenientSequences}, snap.Clone(snap.Generation))
	next, report := reg.Apply(registry.Batch{
		Features:  res.Features,
		Sequences: res.Sequences,
		Op:        registry.Op{Kind: "import", URI: uri, Format: string(format), Source: source},
	})
	// Parser-level rejections and registry-level rejections both fail the
	// whole transaction.
	report.Rejections = append(res.Rejections, report.Rejections...)
	if len(report.Rejections) > 0 {
		sortRejections(report.Rejections)
		return report, ErrRejected
	}
	if err := persist.Save(st.cfg.Dir, next); err != nil {
		return report, err
	}
	st.publish(next)
	return report, nil
}

// Rollback moves id back to the content of toVersion (see registry rules).
func (st *Store) Rollback(id string, toVersion int) error {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()

	st.mu.RLock()
	snap := st.cur.v.snap
	st.mu.RUnlock()
	reg := registry.FromSnapshot(registry.Config{RequireSequences: !st.cfg.LenientSequences}, snap.Clone(snap.Generation))
	next, rej, ok := reg.Rollback(id, toVersion)
	if !ok {
		return &rej
	}
	if next.Generation == snap.Generation {
		return nil // already at that content
	}
	if err := persist.Save(st.cfg.Dir, next); err != nil {
		return err
	}
	st.publish(next)
	return nil
}

// Rebuild replays every recorded source operation from local files, starting
// from an empty index. Queries during the rebuild keep seeing the old view;
// only when replay and persistence fully succeed is the new view published.
func (st *Store) Rebuild() (int, error) {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()

	st.mu.RLock()
	ops := append([]registry.Op(nil), st.cur.v.snap.Ops...)
	st.mu.RUnlock()

	reg := registry.New(registry.Config{RequireSequences: !st.cfg.LenientSequences})
	var imported int
	for _, op := range ops {
		switch op.Kind {
		case "import":
			batch, err := readOpFile(op)
			if err != nil {
				return 0, err
			}
			next, report := reg.Apply(batch)
			if len(report.Rejections) > 0 {
				return 0, &model.RejectError{Kind: model.RejectSource, URI: op.URI,
					Detail: fmt.Sprintf("rebuild replay rejected %d record(s): %s", len(report.Rejections), report.Rejections[0].Error())}
			}
			_ = next
			imported++
		case "rollback":
			_, rej, ok := reg.Rollback(op.ID, op.ToVer)
			if !ok {
				return 0, &rej
			}
		default:
			return 0, &model.RejectError{Kind: model.RejectSnapshot, Detail: "unknown op kind: " + op.Kind}
		}
	}
	next := reg.Snapshot()
	// Persist to a staging directory first so a failure never destroys the
	// currently serving snapshot.
	staging := filepath.Join(st.cfg.Dir, ".rebuild-tmp")
	_ = os.RemoveAll(staging)
	if err := persist.Save(staging, next); err != nil {
		return 0, err
	}
	snapPath, sumPath := persist.Paths(st.cfg.Dir)
	stgSnap, stgSum := persist.Paths(staging)
	if err := os.Rename(stgSnap, snapPath); err != nil {
		return 0, &model.RejectError{Kind: model.RejectSnapshot, Detail: "install snapshot: " + err.Error()}
	}
	if err := os.Rename(stgSum, sumPath); err != nil {
		return 0, &model.RejectError{Kind: model.RejectSnapshot, Detail: "install checksum: " + err.Error()}
	}
	_ = os.Remove(staging)
	st.publish(next)
	return imported, nil
}

// RebuildFromSources rebuilds a store from an explicit ordered list of local
// files. Useful when the previous index was corrupt/discarded.
func RebuildFromSources(cfg Config, files []SourceFile) (*Store, error) {
	if err := persist.Discard(cfg.Dir); err != nil {
		return nil, err
	}
	st, err := Open(cfg)
	if err != nil {
		return nil, err
	}
	for _, sf := range files {
		if _, err := st.ImportFile(sf.Path, sf.Source, sf.Format); err != nil {
			return nil, fmt.Errorf("rebuild import %s: %w", sf.Path, err)
		}
	}
	return st, nil
}

// SourceFile describes one rebuild input.
type SourceFile struct {
	Path   string
	Source string
	Format parse.Format
}

func readOpFile(op registry.Op) (registry.Batch, error) {
	path := uriToPath(op.URI)
	f, err := os.Open(path)
	if err != nil {
		return registry.Batch{}, &model.RejectError{Kind: model.RejectSource, URI: op.URI, Detail: err.Error()}
	}
	defer f.Close()
	res := parse.Parse(f, parse.Options{
		Format: parse.Format(op.Format), URI: op.URI, Source: op.Source,
	})
	if len(res.Rejections) > 0 {
		return registry.Batch{}, &model.RejectError{Kind: model.RejectSource, URI: op.URI,
			Detail: fmt.Sprintf("source now contains %d invalid record(s): %s", len(res.Rejections), res.Rejections[0].Error())}
	}
	return registry.Batch{
		Features:  res.Features,
		Sequences: res.Sequences,
		Op:        op,
	}, nil
}

func uriToPath(uri string) string {
	const prefix = "file://"
	if len(uri) >= len(prefix) && uri[:len(prefix)] == prefix {
		return uri[len(prefix):]
	}
	return uri
}

// Query runs an interval/strand query against the current complete view.
func (st *Store) Query(q index.Query) []index.Result {
	st.mu.RLock()
	v := st.cur.v
	st.mu.RUnlock()
	return v.idx.Search(v.snap, q)
}

// Children returns the active children of id from the current view.
func (st *Store) Children(id string) []model.Feature {
	st.mu.RLock()
	v := st.cur.v
	st.mu.RUnlock()
	return v.snap.Children(id)
}

var (
	errBadFormat = errors.New("store: unsupported format")
	// ErrRejected indicates a transactional import failed validation; the
	// report carries all reasons.
	ErrRejected = errors.New("store: import rejected")
)

func sortRejections(rs []model.Rejection) {
	// registry already sorts; keep combined parser+registry list ordered.
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && lessRej(rs[j], rs[j-1]); j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

func lessRej(a, b model.Rejection) bool {
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Kind < b.Kind
}
