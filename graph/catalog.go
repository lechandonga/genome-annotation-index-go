package graph

import (
	"sort"

	"github.com/lechandonga/genome-annotation-index-go/model"
)

// OnConflict defines how a new record for an id already present is handled.
type OnConflict int

const (
	// ConflictReject refuses the whole batch (deterministic default).
	ConflictReject OnConflict = iota
	// ConflictOverwrite replaces an existing unversioned feature; for
	// versioned features the same VersionEvolution rules still apply.
	ConflictOverwrite
)

// ApplyOptions controls one catalog change.
type ApplyOptions struct {
	OnConflict OnConflict
}

// Catalog is the mutable working set used to build snapshots.
type Catalog struct {
	revision Revision
	snap     *Snapshot
}

// NewCatalog starts with an empty snapshot at revision 0.
func NewCatalog() *Catalog {
	return &Catalog{
		revision: 0,
		snap: &Snapshot{
			Features: map[string][]VersionedFeature{},
			Children: map[string][]string{},
		},
	}
}

// Revision returns the current revision number.
func (c *Catalog) Revision() Revision { return c.revision }

// Snapshot returns the current immutable snapshot.
func (c *Catalog) Snapshot() *Snapshot { return c.snap }

// Apply validates and commits one batch of features atomically: either every
// feature is accepted and the revision advances, or nothing changes.
func (c *Catalog) Apply(features []model.Feature, opts ApplyOptions) (*Snapshot, Problems) {
	next := c.snap.Clone()
	next.Revision = c.revision + 1

	// Deterministic ordering makes conflict reporting reproducible.
	batch := append([]model.Feature(nil), features...)
	sortFeatures(batch)

	indexed := map[string]int{} // id -> index of current version in next.Features
	for id, vs := range next.Features {
		indexed[id] = len(vs) - 1
	}

	var problems Problems
	newInBatch := map[string]bool{}

	for _, f := range batch {
		f := f
		f.ParentIDs = dedupeSortedCopy(f.ParentIDs)

		vs, exists := next.Features[f.ID]
		switch {
		case !exists:
			next.Features[f.ID] = []VersionedFeature{{Feature: f, Current: true}}
			newInBatch[f.ID] = true
		case f.Version == "" && opts.OnConflict == ConflictOverwrite && allUnversioned(vs):
			next.Features[f.ID] = []VersionedFeature{{Feature: f, Current: true}}
		default:
			// Versioned evolution or duplicate detection.
			if probs := addVersion(next, f, opts, newInBatch[f.ID]); len(probs) > 0 {
				problems = append(problems, probs...)
			}
		}
	}

	if len(problems) > 0 {
		return c.snap, problems
	}

	// Rebuild current flags.
	for id, vs := range next.Features {
		for i := range vs {
			vs[i].Current = i == len(vs)-1
		}
		next.Features[id] = vs
	}

	if probs := validateRelations(next); len(probs) > 0 {
		return c.snap, probs
	}

	rebuildChildren(next)
	c.snap = next
	c.revision = next.Revision
	return c.snap, nil
}

func addVersion(next *Snapshot, f model.Feature, opts ApplyOptions, freshInBatch bool) Problems {
	var problems Problems
	vs := next.Features[f.ID]
	for _, v := range vs {
		cmp := CompareVersions(v.Feature.Version, f.Version)
		switch {
		case cmp == 0:
			if f.Version == "" {
				problems = append(problems, &Problem{
					Code: ProbDuplicateID, Feature: f.ID,
					Message: "unversioned feature id appears more than once",
				})
				return problems
			}
			problems = append(problems, &Problem{
				Code: ProbDuplicateVersion, Feature: f.ID, Related: f.Version,
				Message: "feature version already exists; choose a higher version or use overwrite",
			})
			return problems
		case cmp > 0:
			problems = append(problems, &Problem{
				Code: ProbVersionRegression, Feature: f.ID,
				Related: f.Version,
				Message: "new version is not higher than retained version " + v.Feature.Version,
			})
			return problems
		}
	}
	vs = append(vs, VersionedFeature{Feature: f})
	sort.SliceStable(vs, func(i, j int) bool {
		return CompareVersions(vs[i].Feature.Version, vs[j].Feature.Version) < 0
	})
	next.Features[f.ID] = vs
	return problems
}

func allUnversioned(vs []VersionedFeature) bool {
	for _, v := range vs {
		if v.Feature.Version != "" {
			return false
		}
	}
	return true
}

func validateRelations(s *Snapshot) Problems {
	var problems Problems

	// 1. dangling parents, self references, parent type ordering.
	for id, vs := range s.Features {
		f := vs[len(vs)-1].Feature
		for _, pid := range f.ParentIDs {
			if pid == id {
				problems = append(problems, &Problem{
					Code: ProbSelfReference, Feature: id, Related: pid,
					Message: "feature lists itself as parent",
				})
				continue
			}
			pvs, ok := s.Features[pid]
			if !ok {
				problems = append(problems, &Problem{
					Code: ProbDanglingParent, Feature: id, Related: pid,
					Message: "parent id does not exist in the catalog",
				})
				continue
			}
			parent := pvs[len(pvs)-1].Feature
			if !validParentType(f.Type, parent.Type) {
				problems = append(problems, &Problem{
					Code: ProbBadParentType, Feature: id, Related: pid,
					Message: string(f.Type) + " cannot have parent of type " + string(parent.Type),
				})
			}
		}
	}

	// 2. cycle detection over current parent edges (deterministic DFS).
	ids := make([]string, 0, len(s.Features))
	for id := range s.Features {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var cycleIDs []string
	var dfs func(string) bool
	dfs = func(id string) bool {
		color[id] = gray
		f, ok := s.Current(id)
		if ok {
			parents := append([]string(nil), f.ParentIDs...)
			sort.Strings(parents)
			for _, pid := range parents {
				if _, exists := s.Features[pid]; !exists {
					continue
				}
				switch color[pid] {
				case gray:
					cycleIDs = []string{pid, id}
					return true
				case white:
					if dfs(pid) {
						cycleIDs = append(cycleIDs, id)
						return true
					}
				}
			}
		}
		color[id] = black
		return false
	}
	for _, id := range ids {
		if color[id] == white && dfs(id) {
			sort.Strings(cycleIDs)
			problems = append(problems, &Problem{
				Code: ProbCycle, Feature: cycleIDs[0],
				Related: cycleIDs[len(cycleIDs)-1],
				Message: "parent graph contains a cycle",
			})
			break
		}
	}

	sortProblems(problems)
	return problems
}

func validParentType(child, parent model.FeatureType) bool {
	switch child {
	case model.Gene:
		return parent != model.Transcript && parent != model.Exon && parent != model.CDS
	case model.Transcript:
		return parent == model.Gene || parent == model.Other
	case model.Exon, model.CDS:
		return parent == model.Transcript || parent == model.Gene || parent == model.Other
	default:
		return true
	}
}

func rebuildChildren(s *Snapshot) {
	s.Children = map[string][]string{}
	for id, vs := range s.Features {
		f := vs[len(vs)-1].Feature
		for _, pid := range f.ParentIDs {
			s.Children[pid] = append(s.Children[pid], id)
		}
	}
	for pid := range s.Children {
		sort.Strings(s.Children[pid])
	}
}

func dedupeSortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	w := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[w-1] {
			out[w] = out[i]
			w++
		}
	}
	return out[:w]
}

func sortProblems(ps Problems) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Code != ps[j].Code {
			return ps[i].Code < ps[j].Code
		}
		if ps[i].Feature != ps[j].Feature {
			return ps[i].Feature < ps[j].Feature
		}
		return ps[i].Related < ps[j].Related
	})
}

// Replace installs an externally built snapshot (used by rebuild/replay).
// The caller guarantees integrity; relations are re-validated.
func (c *Catalog) Replace(s *Snapshot) Problems {
	if probs := validateRelations(s); len(probs) > 0 {
		return probs
	}
	rebuildChildren(s)
	c.snap = s
	c.revision = s.Revision
	return nil
}
