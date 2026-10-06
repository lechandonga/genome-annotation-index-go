package index_test

import (
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/pkg/index"
	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/registry"
	"github.com/lechandonga/genome-annotation-index-go/pkg/testutil"
)

func buildReg(t *testing.T) *registry.Registry {
	r := registry.New(registry.Config{RequireSequences: false})
	feats := []model.Feature{
		{ID: "a", Type: model.FeatureGene, SeqID: "c", Start: 0, End: 10, Strand: "+"},
		{ID: "b", Type: model.FeatureExon, SeqID: "c", Start: 10, End: 20, Strand: "-"},
		{ID: "d", Type: model.FeatureGene, SeqID: "c", Start: 20, End: 30, Strand: "-"},
		{ID: "e", Type: model.FeatureGene, SeqID: "other", Start: 0, End: 5, Strand: "+"},
	}
	for i := range feats {
		feats[i].Attrs = map[string]string{}
	}
	_, rep := r.Apply(registry.Batch{Features: feats})
	if len(rep.Rejections) != 0 {
		t.Fatalf("seed: %v", rep.Rejections)
	}
	return r
}

func TestIntervalAndStrand(t *testing.T) {
	r := buildReg(t)
	x := index.Build(r.Snapshot())

	testutil.RunGroup(t, "query.interval", []testutil.Case{
		{
			Name: "half-open boundary: touching intervals do not overlap",
			Run: func() string {
				// query [10,20) must not include feature a [0,10)
				hits := x.Search(r.Snapshot(), index.Query{SeqID: "c", Start: 10, End: 20})
				if ids(hits) != "b" {
					return testutil.Fail("hits=%s", ids(hits))
				}
				// empty point query at 10
				hits = x.Search(r.Snapshot(), index.Query{SeqID: "c", Start: 30, End: 30})
				if len(hits) != 0 {
					return testutil.Fail("point query returned %s", ids(hits))
				}
				return ""
			},
		},
		{
			Name: "partial overlaps at both ends",
			Run: func() string {
				hits := x.Search(r.Snapshot(), index.Query{SeqID: "c", Start: 5, End: 25})
				if ids(hits) != "a,b,d" {
					return testutil.Fail("hits=%s", ids(hits))
				}
				return ""
			},
		},
		{
			Name: "strand filter + and -",
			Run: func() string {
				plus := x.Search(r.Snapshot(), index.Query{SeqID: "c", Start: 0, End: 100, StrandFilter: "+"})
				if ids(plus) != "a" {
					return testutil.Fail("plus=%s", ids(plus))
				}
				minus := x.Search(r.Snapshot(), index.Query{SeqID: "c", Start: 0, End: 100, StrandFilter: "-"})
				if ids(minus) != "d,b" {
					return testutil.Fail("minus order should be descending start, got %s", ids(minus))
				}
				return ""
			},
		},
		{
			Name: "type filter",
			Run: func() string {
				hits := x.Search(r.Snapshot(), index.Query{
					SeqID: "c", Start: 0, End: 100, Types: []model.FeatureType{model.FeatureGene},
				})
				if ids(hits) != "a,d" {
					return testutil.Fail("hits=%s", ids(hits))
				}
				return ""
			},
		},
		{
			Name: "unknown sequence returns no hit",
			Run: func() string {
				if len(x.Search(r.Snapshot(), index.Query{SeqID: "zz", Start: 0, End: 1})) != 0 {
					return "expected empty"
				}
				return ""
			},
		},
	})
}

func ids(hits []index.Result) string {
	out := ""
	for i, h := range hits {
		if i > 0 {
			out += ","
		}
		out += h.Feature.ID
	}
	return out
}
