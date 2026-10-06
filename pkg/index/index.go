// Package index provides immutable interval indexes over registry snapshots.
// Each snapshot gets a freshly built Index; old indexes are discarded
// atomically when a new snapshot is published, so concurrent readers always
// see one complete view.
package index

import (
	"sort"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/registry"
)

// entry is one indexed feature reference.
type entry struct {
	start int
	end   int
	id    string
}

// Index is an immutable interval structure for one snapshot generation.
type Index struct {
	generation int
	// per sequence, entries sorted by start then end then id.
	seqs map[string][]entry
}

// Build constructs an index from a snapshot.
func Build(s *registry.Snapshot) *Index {
	idx := &Index{generation: s.Generation, seqs: map[string][]entry{}}
	for _, f := range s.Features() {
		idx.seqs[f.SeqID] = append(idx.seqs[f.SeqID], entry{start: f.Start, end: f.End, id: f.ID})
	}
	for seq := range idx.seqs {
		es := idx.seqs[seq]
		sort.Slice(es, func(i, j int) bool {
			if es[i].start != es[j].start {
				return es[i].start < es[j].start
			}
			if es[i].end != es[j].end {
				return es[i].end < es[j].end
			}
			return es[i].id < es[j].id
		})
		idx.seqs[seq] = es
	}
	return idx
}

// Generation returns the snapshot generation this index was built from.
func (x *Index) Generation() int { return x.generation }

// Query restricts results by interval and optionally by strand.
type Query struct {
	SeqID string
	Start int
	End   int
	// StrandFilter: "" or "." returns both strands; "+" / "-" filters.
	StrandFilter string
	// Types optionally restricts feature types.
	Types []model.FeatureType
}

// Result is one query hit, carrying the active feature.
type Result struct {
	Feature model.Feature
}

// Search returns features overlapping [start,end) on the sequence.
// Ordering is deterministic:
//
//   - forward / unstranded query: ascending start, then end, then id
//   - reverse-strand query: descending start, then descending end, then id
//
// The reverse order reflects 5'->3' traversal on the minus strand.
func (x *Index) Search(s *registry.Snapshot, q Query) []Result {
	entries := x.seqs[q.SeqID]
	if len(entries) == 0 {
		return nil
	}
	// First entry with start < q.End: because intervals overlap iff
	// e.start < q.End && q.Start < e.end, an entry starting at/after q.End
	// cannot overlap. Upper bound on start.
	limit := sort.Search(len(entries), func(i int) bool { return entries[i].start >= q.End })
	var out []Result
	typeSet := map[model.FeatureType]bool{}
	for _, t := range q.Types {
		typeSet[t] = true
	}
	for i := 0; i < limit; i++ {
		e := entries[i]
		if q.Start >= e.end {
			continue
		}
		f, ok := s.CurrentFeature(e.id)
		if !ok {
			continue
		}
		if q.StrandFilter == model.StrandPlus || q.StrandFilter == model.StrandMinus {
			if f.Strand != q.StrandFilter {
				continue
			}
		}
		if len(typeSet) > 0 && !typeSet[f.Type] {
			continue
		}
		out = append(out, Result{Feature: f})
	}
	if q.StrandFilter == model.StrandMinus {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}
