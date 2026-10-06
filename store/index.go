package store

import (
	"sort"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

// StrandFilter selects features by strand.
type StrandFilter int

const (
	// StrandAny returns features on either strand and unstranded ones.
	StrandAny StrandFilter = iota
	// StrandForward returns + and (when includeUnstranded) '.' features.
	StrandForward
	// StrandReverse returns - and (when includeUnstranded) '.' features.
	StrandReverse
	// StrandForwardOnly returns only + features.
	StrandForwardOnly
	// StrandReverseOnly returns only - features.
	StrandReverseOnly
)

// Query is an interval query request.
type Query struct {
	SeqID  string
	Region coord.Interval
	Strand StrandFilter
	Types  []model.FeatureType
	// IncludeLineage adds transitive ancestors of every matched feature when
	// those ancestors overlap the queried region.
	IncludeLineage bool
}

type seqIndex struct {
	features []model.Feature // sorted by start, then end, then id
}

func buildSeqIndex(fs []model.Feature) *seqIndex {
	sorted := append([]model.Feature(nil), fs...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Interval.Start != b.Interval.Start {
			return a.Interval.Start < b.Interval.Start
		}
		if a.Interval.End != b.Interval.End {
			return a.Interval.End < b.Interval.End
		}
		return a.ID < b.ID
	})
	return &seqIndex{features: sorted}
}

// overlaps returns all features overlapping [start,end). The slice is sorted
// by start, so candidates are those with start < end; once starts >= end all
// later rows are non-overlapping.
func (x *seqIndex) overlaps(region coord.Interval) []model.Feature {
	var out []model.Feature
	for i := 0; i < len(x.features); i++ {
		f := x.features[i]
		if f.Interval.Start >= region.End {
			break
		}
		if f.Interval.End > region.Start {
			out = append(out, f)
		}
	}
	return out
}

func matchesStrand(s model.Strand, f StrandFilter) bool {
	switch f {
	case StrandAny:
		return true
	case StrandForward:
		return s == model.Forward || s == model.Unstranded
	case StrandReverse:
		return s == model.Reverse || s == model.Unstranded
	case StrandForwardOnly:
		return s == model.Forward
	case StrandReverseOnly:
		return s == model.Reverse
	default:
		return true
	}
}

func matchesType(t model.FeatureType, want []model.FeatureType) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if t == w {
			return true
		}
	}
	return false
}

// querySnapshot runs an interval query against an immutable snapshot.
func querySnapshot(snap *graph.Snapshot, q Query) []model.FeatureRef {
	bySeq := map[string][]model.Feature{}
	for _, f := range snap.AllCurrent() {
		bySeq[f.SeqID] = append(bySeq[f.SeqID], f)
	}
	idx := buildSeqIndex(bySeq[q.SeqID])
	hits := idx.overlaps(q.Region)

	resultIDs := map[string]bool{}
	var out []model.Feature
	add := func(f model.Feature) {
		if !resultIDs[f.ID] && matchesStrand(f.Strand, q.Strand) && matchesType(f.Type, q.Types) {
			resultIDs[f.ID] = true
			out = append(out, f)
		}
	}

	for _, f := range hits {
		add(f)
		if q.IncludeLineage {
			for _, anc := range snap.Ancestors(f.ID) {
				if anc.SeqID == q.SeqID && anc.Interval.Overlaps(q.Region) {
					add(anc)
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Interval.Start != b.Interval.Start {
			return a.Interval.Start < b.Interval.Start
		}
		if a.Interval.End != b.Interval.End {
			return a.Interval.End < b.Interval.End
		}
		return a.ID < b.ID
	})
	refs := make([]model.FeatureRef, len(out))
	for i := range out {
		refs[i] = out[i].Ref()
	}
	return refs
}
