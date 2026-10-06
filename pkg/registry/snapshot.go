// Package registry builds and validates the parent/child feature graph,
// enforces identity/version rules and produces immutable snapshots that the
// index layer can publish atomically.
package registry

import (
	"sort"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// Snapshot is an immutable, queryable view of all accepted features.
type Snapshot struct {
	Generation int                        `json:"generation"`
	Versions   map[string][]model.Feature `json:"versions"`
	Current    map[string]int             `json:"current"`
	Sequences  map[string]model.Sequence  `json:"sequences"`
	Ops        []Op                       `json:"ops"`
}

// Op is one replayable mutation used for snapshot rebuild.
type Op struct {
	Kind   string `json:"kind"`
	URI    string `json:"uri,omitempty"`
	Format string `json:"format,omitempty"`
	Source string `json:"source,omitempty"`
	ID     string `json:"id,omitempty"`
	ToVer  int    `json:"to_version,omitempty"`
}

// CurrentFeature returns the active version of id.
func (s *Snapshot) CurrentFeature(id string) (model.Feature, bool) {
	idx, ok := s.Current[id]
	if !ok {
		return model.Feature{}, false
	}
	return s.Versions[id][idx], true
}

// Features returns active features ordered by id for deterministic output.
func (s *Snapshot) Features() []model.Feature {
	ids := make([]string, 0, len(s.Current))
	for id := range s.Current {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]model.Feature, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.Versions[id][s.Current[id]])
	}
	return out
}

// Children returns active features whose Parent list contains parentID.
func (s *Snapshot) Children(parentID string) []model.Feature {
	var out []model.Feature
	for _, f := range s.Features() {
		for _, p := range f.Parents {
			if p == parentID {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// Clone returns a deep copy so snapshots stay immutable after publishing.
func (s *Snapshot) Clone(generation int) *Snapshot {
	c := &Snapshot{
		Generation: generation,
		Versions:   make(map[string][]model.Feature, len(s.Versions)),
		Current:    make(map[string]int, len(s.Current)),
		Sequences:  make(map[string]model.Sequence, len(s.Sequences)),
		Ops:        append([]Op(nil), s.Ops...),
	}
	for k, v := range s.Versions {
		fs := make([]model.Feature, len(v))
		for i := range v {
			fs[i] = cloneFeature(v[i])
		}
		c.Versions[k] = fs
	}
	for k, v := range s.Current {
		c.Current[k] = v
	}
	for k, v := range s.Sequences {
		c.Sequences[k] = v
	}
	return c
}

func cloneFeature(f model.Feature) model.Feature {
	f.Parents = append([]string(nil), f.Parents...)
	attrs := make(map[string]string, len(f.Attrs))
	for k, v := range f.Attrs {
		attrs[k] = v
	}
	f.Attrs = attrs
	return f
}

func emptySnapshot() *Snapshot {
	return &Snapshot{
		Versions:  map[string][]model.Feature{},
		Current:   map[string]int{},
		Sequences: map[string]model.Sequence{},
	}
}
