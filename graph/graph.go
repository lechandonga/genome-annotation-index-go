// Package graph builds and validates the parent/child hierarchy of parsed
// features, maintains version history and produces immutable snapshots that
// the query layer can atomically swap.
//
// All conflict and integrity problems (duplicate ids, unknown parents,
// cycles, invalid parent types, version regressions) are returned as
// structured errors; nothing is silently discarded.
package graph

import (
	"fmt"
	"sort"

	"github.com/lechandonga/genome-annotation-index-go/model"
)

// Revision labels one committed state of the catalog.
type Revision int64

// VersionedFeature is one historical version of a feature id.
type VersionedFeature struct {
	Feature model.Feature
	// Current is true for the highest version retained for the id.
	Current bool
}

// Snapshot is an immutable view of a committed catalog state.
type Snapshot struct {
	Revision Revision
	// Features maps id -> all retained versions, sorted ascending.
	Features map[string][]VersionedFeature
	// Children maps parent id -> child ids (sorted).
	Children map[string][]string
}

// Current returns the current (highest) version for id, if any.
func (s *Snapshot) Current(id string) (model.Feature, bool) {
	vs, ok := s.Features[id]
	if !ok || len(vs) == 0 {
		return model.Feature{}, false
	}
	return vs[len(vs)-1].Feature, true
}

// AllCurrent returns every feature that is the current version of its id,
// sorted by (SeqID, Start, End, ID) for deterministic iteration.
func (s *Snapshot) AllCurrent() []model.Feature {
	out := make([]model.Feature, 0, len(s.Features))
	for _, vs := range s.Features {
		out = append(out, vs[len(vs)-1].Feature)
	}
	sortFeatures(out)
	return out
}

// Ancestors returns the transitive parent chain of id starting with the
// immediate parent. Cycles are broken deterministically.
func (s *Snapshot) Ancestors(id string) []model.Feature {
	seen := map[string]bool{id: true}
	var out []model.Feature
	cur, ok := s.Current(id)
	if !ok {
		return nil
	}
	queue := append([]string(nil), cur.ParentIDs...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		p, ok := s.Current(pid)
		if !ok {
			continue
		}
		seen[pid] = true
		out = append(out, p)
		queue = append(queue, p.ParentIDs...)
	}
	return out
}

func sortFeatures(fs []model.Feature) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.SeqID != b.SeqID {
			return a.SeqID < b.SeqID
		}
		if a.Interval.Start != b.Interval.Start {
			return a.Interval.Start < b.Interval.Start
		}
		if a.Interval.End != b.Interval.End {
			return a.Interval.End < b.Interval.End
		}
		return a.ID < b.ID
	})
}

// Clone returns a deep copy of the snapshot's mutable maps.
func (s *Snapshot) Clone() *Snapshot {
	c := &Snapshot{
		Revision: s.Revision,
		Features: make(map[string][]VersionedFeature, len(s.Features)),
		Children: make(map[string][]string, len(s.Children)),
	}
	for id, vs := range s.Features {
		cp := make([]VersionedFeature, len(vs))
		for i, v := range vs {
			cp[i] = deepCopyVF(v)
		}
		c.Features[id] = cp
	}
	for id, kids := range s.Children {
		c.Children[id] = append([]string(nil), kids...)
	}
	return c
}

func deepCopyVF(v VersionedFeature) VersionedFeature {
	f := v.Feature
	f.ParentIDs = append([]string(nil), f.ParentIDs...)
	if f.Attrs != nil {
		f.Attrs = cloneAttrs(f.Attrs)
	}
	return VersionedFeature{Feature: f, Current: v.Current}
}

func cloneAttrs(a map[string]string) map[string]string {
	b := make(map[string]string, len(a))
	for k, v := range a {
		b[k] = v
	}
	return b
}

// Problem is a structured graph integrity error.
type Problem struct {
	Code    string
	Feature string
	Related string
	Message string
}

func (p *Problem) Error() string {
	if p.Related != "" {
		return fmt.Sprintf("%s: %s (feature=%s, related=%s)", p.Code, p.Message, p.Feature, p.Related)
	}
	return fmt.Sprintf("%s: %s (feature=%s)", p.Code, p.Message, p.Feature)
}

// Problem codes.
const (
	ProbDuplicateID       = "duplicate_id"
	ProbDuplicateVersion  = "duplicate_version"
	ProbVersionRegression = "version_regression"
	ProbDanglingParent    = "dangling_parent"
	ProbCycle             = "cycle"
	ProbBadParentType     = "bad_parent_type"
	ProbSelfReference     = "self_reference"
)

// Problems is a batch of integrity problems.
type Problems []*Problem

func (ps Problems) Error() string {
	if len(ps) == 0 {
		return ""
	}
	return fmt.Sprintf("%d graph problem(s), first: %s", len(ps), ps[0].Error())
}

// Codes groups problems by code.
func (ps Problems) Codes() map[string]int {
	out := map[string]int{}
	for _, p := range ps {
		out[p.Code]++
	}
	return out
}
