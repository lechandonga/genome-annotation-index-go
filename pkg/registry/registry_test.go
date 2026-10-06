package registry_test

import (
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/registry"
	"github.com/lechandonga/genome-annotation-index-go/pkg/testutil"
)

func seq() []model.Sequence {
	return []model.Sequence{{ID: "chr1", Length: 1000, SHA256: "a", URI: "file:///f", Source: "s"}}
}

func feat(id string, typ model.FeatureType, start, end int, parents ...string) model.Feature {
	return model.Feature{
		ID: id, Version: 1, Type: typ, SeqID: "chr1",
		Start: start, End: end, Strand: "+", Parents: parents,
		URI: "file:///f", Line: 1, Attrs: map[string]string{},
	}
}

func TestHierarchyConflicts(t *testing.T) {
	testutil.RunGroup(t, "registry.graph", []testutil.Case{
		{
			Name: "valid gene-transcript-exon graph accepted",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep := r.Apply(registry.Batch{
					Sequences: seq(),
					Features: []model.Feature{
						feat("g1", model.FeatureGene, 0, 100),
						feat("t1", model.FeatureTranscript, 0, 100, "g1"),
						feat("e1", model.FeatureExon, 0, 50, "t1"),
					},
				})
				if len(rep.Rejections) != 0 {
					return testutil.Fail("rejections: %v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "dangling parent reference rejected",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep := r.Apply(registry.Batch{
					Sequences: seq(),
					Features:  []model.Feature{feat("t1", model.FeatureTranscript, 0, 100, "ghost")},
				})
				if !has(rep.Rejections, model.RejectDanglingRef) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				if len(r.Snapshot().Current) != 0 {
					return "snapshot changed despite rejection"
				}
				return ""
			},
		},
		{
			Name: "circular parent chain rejected",
			Run: func() string {
				r := registry.New(registry.Config{RequireSequences: false})
				_, rep := r.Apply(registry.Batch{
					Features: []model.Feature{
						feat("a", model.FeatureOther, 0, 10, "b"),
						feat("b", model.FeatureOther, 0, 10, "a"),
					},
				})
				if !has(rep.Rejections, model.RejectCircularRef) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "wrong parent type (exon under gene) rejected",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep := r.Apply(registry.Batch{
					Sequences: seq(),
					Features: []model.Feature{
						feat("g1", model.FeatureGene, 0, 100),
						feat("e1", model.FeatureExon, 0, 50, "g1"),
					},
				})
				if !has(rep.Rejections, model.RejectTypeConflict) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "duplicate id in one batch rejected",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep := r.Apply(registry.Batch{
					Sequences: seq(),
					Features: []model.Feature{
						feat("g1", model.FeatureGene, 0, 100),
						feat("g1", model.FeatureGene, 10, 20),
					},
				})
				if !has(rep.Rejections, model.RejectDuplicateID) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "type change for same id rejected",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep1 := r.Apply(registry.Batch{Sequences: seq(), Features: []model.Feature{feat("x", model.FeatureGene, 0, 10)}})
				if len(rep1.Rejections) != 0 {
					return testutil.Fail("first apply: %v", rep1.Rejections)
				}
				_, rep2 := r.Apply(registry.Batch{Features: []model.Feature{feat("x", model.FeatureTranscript, 0, 10)}})
				if !has(rep2.Rejections, model.RejectTypeConflict) {
					return testutil.Fail("got %v", rep2.Rejections)
				}
				return ""
			},
		},
		{
			Name: "out-of-bounds vs sequence length rejected",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep := r.Apply(registry.Batch{
					Sequences: seq(),
					Features:  []model.Feature{feat("g1", model.FeatureGene, 0, 5000)},
				})
				if !has(rep.Rejections, model.RejectOutOfBounds) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "conflicting sequence redefinition rejected",
			Run: func() string {
				r := registry.New(registry.DefaultConfig())
				_, rep := r.Apply(registry.Batch{
					Sequences: []model.Sequence{
						{ID: "c", Length: 10, SHA256: "a"},
						{ID: "c", Length: 11, SHA256: "b"},
					},
				})
				if !has(rep.Rejections, model.RejectSeqConflict) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				return ""
			},
		},
	})
}

func TestVersioning(t *testing.T) {
	testutil.RunGroup(t, "registry.versioning", []testutil.Case{
		{
			Name: "auto-increment on changed re-import; identical content is no-op",
			Run: func() string {
				r := registry.New(registry.Config{RequireSequences: false})
				_, rep := r.Apply(registry.Batch{Features: []model.Feature{feat("g1", model.FeatureGene, 0, 10)}})
				if rep.Accepted != 1 {
					return testutil.Fail("accepted=%d", rep.Accepted)
				}
				_, rep = r.Apply(registry.Batch{Features: []model.Feature{feat("g1", model.FeatureGene, 0, 10)}})
				if rep.Noop != 1 || rep.Accepted != 0 {
					return testutil.Fail("expected noop, got %+v", rep)
				}
				_, rep = r.Apply(registry.Batch{Features: []model.Feature{feat("g1", model.FeatureGene, 0, 20)}})
				if rep.Accepted != 1 {
					return testutil.Fail("expected new version, got %+v", rep)
				}
				cur, _ := r.Snapshot().CurrentFeature("g1")
				if cur.Version != 2 || cur.End != 20 {
					return testutil.Fail("cur=%+v", cur)
				}
				if len(r.Snapshot().Versions["g1"]) != 2 {
					return "old version history lost"
				}
				return ""
			},
		},
		{
			Name: "explicit version regression rejected",
			Run: func() string {
				r := registry.New(registry.Config{RequireSequences: false})
				f := feat("g1", model.FeatureGene, 0, 10)
				f.Attrs = map[string]string{"version": "2"}
				_, rep := r.Apply(registry.Batch{Features: []model.Feature{f}})
				if len(rep.Rejections) != 0 {
					return testutil.Fail("seed: %v", rep.Rejections)
				}
				old := feat("g1", model.FeatureGene, 0, 9)
				old.Attrs = map[string]string{"version": "1"}
				_, rep = r.Apply(registry.Batch{Features: []model.Feature{old}})
				if !has(rep.Rejections, model.RejectVersionRegress) {
					return testutil.Fail("got %v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "rollback appends prior content at a new monotonic version",
			Run: func() string {
				r := registry.New(registry.Config{RequireSequences: false})
				_, _ = r.Apply(registry.Batch{Features: []model.Feature{feat("g1", model.FeatureGene, 0, 10)}})
				_, _ = r.Apply(registry.Batch{Features: []model.Feature{feat("g1", model.FeatureGene, 0, 20)}})
				_, rej, ok := r.Rollback("g1", 1)
				if !ok {
					return testutil.Fail("rollback failed: %v", rej)
				}
				cur, _ := r.Snapshot().CurrentFeature("g1")
				if cur.End != 10 || cur.Version != 3 {
					return testutil.Fail("cur=%+v", cur)
				}
				return ""
			},
		},
		{
			Name: "rollback to unknown version rejected",
			Run: func() string {
				r := registry.New(registry.Config{RequireSequences: false})
				_, _ = r.Apply(registry.Batch{Features: []model.Feature{feat("g1", model.FeatureGene, 0, 10)}})
				_, _, ok := r.Rollback("g1", 9)
				if ok {
					return "rollback unexpectedly succeeded"
				}
				return ""
			},
		},
	})
}

func has(rs []model.Rejection, k model.RejectKind) bool {
	for _, r := range rs {
		if r.Kind == k {
			return true
		}
	}
	return false
}
