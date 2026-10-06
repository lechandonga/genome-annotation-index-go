package registry

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

func reject(kind model.RejectKind, uri string, line int, format string, args ...any) model.Rejection {
	return model.Rejection{Kind: kind, URI: uri, Line: line, Detail: fmt.Sprintf(format, args...)}
}

func itoa(i int) string { return strconv.Itoa(i) }

func sortRejections(rs []model.Rejection) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].URI != rs[j].URI {
			return rs[i].URI < rs[j].URI
		}
		if rs[i].Line != rs[j].Line {
			return rs[i].Line < rs[j].Line
		}
		return rs[i].Kind < rs[j].Kind
	})
}

// explicitVersion reads an optional "version" attribute (GFF3 convention) or
// "version=*" token. Returns 0 when absent, meaning auto-increment.
func explicitVersion(f model.Feature) int {
	if f.Attrs == nil {
		return 0
	}
	v, ok := f.Attrs["version"]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

// equalContent compares everything except the bookkeeping Version field.
func equalContent(a, b model.Feature) bool {
	if a.ID != b.ID || a.Type != b.Type || a.SeqID != b.SeqID ||
		a.Start != b.Start || a.End != b.End || a.Strand != b.Strand {
		return false
	}
	if len(a.Parents) != len(b.Parents) {
		return false
	}
	pa := append([]string(nil), a.Parents...)
	pb := append([]string(nil), b.Parents...)
	sort.Strings(pa)
	sort.Strings(pb)
	for i := range pa {
		if pa[i] != pb[i] {
			return false
		}
	}
	if len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for k, v := range a.Attrs {
		if b.Attrs[k] != v {
			return false
		}
	}
	return true
}

func validateBounds(f model.Feature, s *Snapshot, requireSeq bool) *model.Rejection {
	synthetic := f.Attrs != nil && f.Attrs["synthetic"] == "true"
	if !synthetic {
		if f.Start < 0 || f.End < 0 {
			r := reject(model.RejectOutOfBounds, f.URI, f.Line, "negative coordinate for %q: [%d,%d)", f.ID, f.Start, f.End)
			return &r
		}
		if f.End < f.Start {
			r := reject(model.RejectOutOfBounds, f.URI, f.Line, "end before start for %q: [%d,%d)", f.ID, f.Start, f.End)
			return &r
		}
	}
	if !requireSeq {
		return nil
	}
	seq, ok := s.Sequences[f.SeqID]
	if !ok {
		r := reject(model.RejectOutOfBounds, f.URI, f.Line, "feature %q references unknown sequence %q", f.ID, f.SeqID)
		return &r
	}
	if !synthetic && f.End > seq.Length {
		r := reject(model.RejectOutOfBounds, f.URI, f.Line,
			"feature %q interval [%d,%d) exceeds length of %s (%d)", f.ID, f.Start, f.End, seq.ID, seq.Length)
		return &r
	}
	return nil
}

// validParentType encodes the allowed gene > transcript > exon/CDS hierarchy.
// FeatureOther features may hang off anything but may not be parents of
// hierarchical features.
func validParentType(child, parent model.FeatureType) bool {
	switch parent {
	case model.FeatureGene:
		return child == model.FeatureTranscript || child == model.FeatureOther
	case model.FeatureTranscript:
		return child == model.FeatureExon || child == model.FeatureCDS || child == model.FeatureOther
	case model.FeatureOther:
		return child == model.FeatureOther
	default:
		return false
	}
}

func findCycle(s *Snapshot) string {
	color := map[string]int{} // 0 white, 1 gray, 2 black
	var stack []string
	var dfs func(id string) string
	dfs = func(id string) string {
		color[id] = 1
		stack = append(stack, id)
		f, ok := s.CurrentFeature(id)
		if ok {
			for _, p := range f.Parents {
				if color[p] == 1 {
					start := 0
					for i, x := range stack {
						if x == p {
							start = i
							break
						}
					}
					return "circular parent chain: " + fmt.Sprintf("%v", append(stack[start:], p))
				}
				if color[p] == 0 {
					if msg := dfs(p); msg != "" {
						return msg
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = 2
		return ""
	}
	ids := make([]string, 0, len(s.Current))
	for id := range s.Current {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == 0 {
			if msg := dfs(id); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func fixSyntheticGenes(s *Snapshot, batch []model.Feature) {
	syn := map[string]bool{}
	for _, f := range batch {
		if f.Attrs != nil && f.Attrs["synthetic"] == "true" {
			syn[f.ID] = true
		}
	}
	for id := range syn {
		minSt, maxEnd := -1, 0
		var seqID, strand string
		for _, child := range s.Children(id) {
			if child.Start == 0 && child.End == 0 {
				continue
			}
			if minSt == -1 || child.Start < minSt {
				minSt = child.Start
			}
			if child.End > maxEnd {
				maxEnd = child.End
			}
			seqID = child.SeqID
			strand = child.Strand
		}
		if minSt == -1 {
			continue
		}
		idx := s.Current[id]
		g := s.Versions[id][idx]
		g.Start, g.End, g.SeqID, g.Strand = minSt, maxEnd, seqID, strand
		s.Versions[id][idx] = g
		if seq, ok := s.Sequences[seqID]; ok && maxEnd > seq.Length {
			// Cannot normally happen: children already validated.
			g.End = seq.Length
			s.Versions[id][idx] = g
		}
	}
}
