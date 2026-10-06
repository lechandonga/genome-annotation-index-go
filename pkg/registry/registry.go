package registry

import (
	"sort"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// Config tunes registry strictness.
type Config struct {
	// RequireSequences rejects features referencing sequences not previously
	// imported from FASTA, and features whose interval exceeds the sequence
	// length. Default true when using DefaultConfig.
	RequireSequences bool
}

// DefaultConfig requires reference sequences.
func DefaultConfig() Config { return Config{RequireSequences: true} }

// Registry accumulates changes and produces snapshots.
type Registry struct {
	cfg  Config
	snap *Snapshot
}

// New starts from an empty snapshot at generation 0.
func New(cfg Config) *Registry {
	return &Registry{cfg: cfg, snap: emptySnapshot()}
}

// FromSnapshot adopts an existing (already validated) snapshot.
func FromSnapshot(cfg Config, s *Snapshot) *Registry {
	return &Registry{cfg: cfg, snap: s}
}

// Snapshot returns the current snapshot.
func (r *Registry) Snapshot() *Snapshot { return r.snap }

// ImportReport summarizes an atomic import attempt.
type ImportReport struct {
	Accepted   int               `json:"accepted"`
	Noop       int               `json:"noop"`
	Sequences  int               `json:"sequences_new"`
	Rejections []model.Rejection `json:"rejections"`
}

// Batch is one transactional import: either every record is accepted (plus
// content-identical no-ops) or the snapshot is left completely untouched and
// every rejection reason is returned. Nothing is silently skipped.
type Batch struct {
	Features  []model.Feature
	Sequences []model.Sequence
	Op        Op
}

// Apply validates and commits a batch. On rejection the registry is unchanged.
func (r *Registry) Apply(b Batch) (*Snapshot, ImportReport) {
	next := r.snap.Clone(r.snap.Generation + 1)
	next.Ops = append(next.Ops, b.Op)
	report := ImportReport{}

	// 1) Sequences: duplicate definitions must agree exactly.
	for _, seq := range b.Sequences {
		if seq.ID == "" {
			report.Rejections = append(report.Rejections, reject(model.RejectMissingID, seq.URI, 0, "sequence without id"))
			continue
		}
		if seq.Length < 0 {
			report.Rejections = append(report.Rejections, reject(model.RejectOutOfBounds, seq.URI, 0, "sequence %q has negative length", seq.ID))
			continue
		}
		if old, ok := next.Sequences[seq.ID]; ok {
			if old.Length != seq.Length || old.SHA256 != seq.SHA256 {
				report.Rejections = append(report.Rejections, reject(model.RejectSeqConflict, seq.URI, 0,
					"sequence %s redefined inconsistently: existing len=%d sha=%s vs new len=%d sha=%s",
					seq.ID, old.Length, old.SHA256, seq.Length, seq.SHA256))
			} else {
				report.Noop++
			}
			continue
		}
		next.Sequences[seq.ID] = seq
		report.Sequences++
	}

	// 2) Duplicate ids inside the batch are rejected explicitly.
	batchFirst := map[string]int{}
	dup := map[string]bool{}
	for i, f := range b.Features {
		if f.ID == "" {
			report.Rejections = append(report.Rejections, reject(model.RejectMissingID, f.URI, f.Line, "feature without id"))
			continue
		}
		if first, seen := batchFirst[f.ID]; seen {
			detail := "id repeated within the same import batch"
			if !dup[f.ID] {
				detail = "id already used at line " + itoa(b.Features[first].Line) + " of the same import batch"
			}
			report.Rejections = append(report.Rejections, reject(model.RejectDuplicateID, f.URI, f.Line, "%s: %q", detail, f.ID))
			dup[f.ID] = true
		} else {
			batchFirst[f.ID] = i
		}
	}

	// 3) Per-feature validation and version resolution.
	proposed := map[string]*model.Feature{}
	for i := range b.Features {
		f := b.Features[i]
		if f.ID == "" || dup[f.ID] {
			continue
		}
		if _, seen := proposed[f.ID]; seen {
			continue
		}
		if rej := validateBounds(f, next, r.cfg.RequireSequences); rej != nil {
			report.Rejections = append(report.Rejections, *rej)
			continue
		}
		if hist, ok := next.Versions[f.ID]; ok {
			cur := hist[next.Current[f.ID]]
			if cur.Type != f.Type {
				report.Rejections = append(report.Rejections, reject(model.RejectTypeConflict, f.URI, f.Line,
					"id %q changed type %q -> %q", f.ID, cur.Type, f.Type))
				continue
			}
			proposedVer := explicitVersion(f)
			if proposedVer == 0 {
				proposedVer = cur.Version + 1
			}
			f.Version = proposedVer
			if equalContent(cur, f) {
				report.Noop++
				proposed[f.ID] = nil
				continue
			}
			if proposedVer <= cur.Version {
				report.Rejections = append(report.Rejections, reject(model.RejectVersionRegress, f.URI, f.Line,
					"id %q changed content but proposes version %d <= current %d", f.ID, proposedVer, cur.Version))
				continue
			}
		} else {
			ver := explicitVersion(f)
			if ver == 0 {
				ver = 1
			}
			if ver < 1 {
				report.Rejections = append(report.Rejections, reject(model.RejectVersionRegress, f.URI, f.Line,
					"id %q initial version %d < 1", f.ID, ver))
				continue
			}
			f.Version = ver
		}
		fc := f
		proposed[f.ID] = &fc
	}

	if len(report.Rejections) > 0 {
		sortRejections(report.Rejections)
		return r.snap, report
	}

	// 4) Commit proposals.
	for id, pf := range proposed {
		if pf == nil {
			continue
		}
		next.Versions[id] = append(next.Versions[id], *pf)
		next.Current[id] = len(next.Versions[id]) - 1
		report.Accepted++
	}

	// 5) Derive synthetic gene spans from children (GTF shorthand).
	fixSyntheticGenes(next, b.Features)

	// 6) Graph validation on the resulting view.
	ids := make([]string, 0, len(proposed))
	for id := range proposed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		f, _ := next.CurrentFeature(id)
		seenParent := map[string]bool{}
		for _, p := range f.Parents {
			if seenParent[p] {
				report.Rejections = append(report.Rejections, reject(model.RejectBadAttribute, f.URI, f.Line,
					"feature %q lists parent %q multiple times", f.ID, p))
				continue
			}
			seenParent[p] = true
			parent, ok := next.CurrentFeature(p)
			if !ok {
				report.Rejections = append(report.Rejections, reject(model.RejectDanglingRef, f.URI, f.Line,
					"feature %q references unknown parent %q", f.ID, p))
				continue
			}
			if !validParentType(f.Type, parent.Type) {
				report.Rejections = append(report.Rejections, reject(model.RejectTypeConflict, f.URI, f.Line,
					"feature %q (%s) cannot have parent %q (%s)", f.ID, f.Type, parent.ID, parent.Type))
			}
		}
	}
	if len(report.Rejections) == 0 {
		if cyc := findCycle(next); cyc != "" {
			report.Rejections = append(report.Rejections, reject(model.RejectCircularRef, "", 0, "%s", cyc))
		}
	}
	if len(report.Rejections) > 0 {
		sortRejections(report.Rejections)
		return r.snap, report
	}

	r.snap = next
	return r.snap, report
}

// Rollback appends the content of an existing version of id as the newest
// version, so query answers become identical to those at toVersion while the
// version line stays monotonic.
func (r *Registry) Rollback(id string, toVersion int) (*Snapshot, model.Rejection, bool) {
	hist, ok := r.snap.Versions[id]
	if !ok {
		return r.snap, reject(model.RejectMissingID, "", 0, "no such feature id %q", id), false
	}
	var target model.Feature
	found := false
	for _, f := range hist {
		if f.Version == toVersion {
			target = f
			found = true
		}
	}
	if !found {
		return r.snap, reject(model.RejectVersionRegress, "", 0, "id %q has no version %d", id, toVersion), false
	}
	cur := hist[r.snap.Current[id]]
	if equalContent(cur, target) {
		return r.snap, model.Rejection{}, true
	}
	next := r.snap.Clone(r.snap.Generation + 1)
	target.Version = cur.Version + 1
	next.Versions[id] = append(next.Versions[id], target)
	next.Current[id] = len(next.Versions[id]) - 1
	next.Ops = append(next.Ops, Op{Kind: "rollback", ID: id, ToVer: toVersion})
	r.snap = next
	return r.snap, model.Rejection{}, true
}
