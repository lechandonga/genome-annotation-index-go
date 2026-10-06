package graph

import (
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

func mkFeature(id, parent, version string, t model.FeatureType) model.Feature {
	var parents []string
	if parent != "" {
		parents = []string{parent}
	}
	return model.Feature{
		ID:        id,
		Type:      t,
		SeqID:     "chr1",
		Strand:    model.Forward,
		Interval:  coord.Interval{Start: 0, End: 10},
		ParentIDs: parents,
		Version:   version,
	}
}

func TestCompareVersions(t *testing.T) {
	t.Log("业务: 版本管理 — 数字/点分/空版本比较规则")
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "1", -1},
		{"1", "", 1},
		{"1.9", "1.10", -1},
		{"1.2.1", "1.2", 1},
		{"2", "10", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Fatalf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	t.Log("业务: 标识冲突 — 无版本重复id整批拒绝")
	c := NewCatalog()
	_, probs := c.Apply([]model.Feature{
		mkFeature("g1", "", "", model.Gene),
		mkFeature("g1", "", "", model.Gene),
	}, ApplyOptions{})
	if probs == nil || probs.Codes()[ProbDuplicateID] != 1 {
		t.Fatalf("want duplicate_id, got %v", probs)
	}
	if c.Revision() != 0 {
		t.Fatal("failed batch must not advance revision")
	}
}

func TestOverwritePolicy(t *testing.T) {
	t.Log("业务: 标识冲突 — overwrite 策略显式替换")
	c := NewCatalog()
	if _, probs := c.Apply([]model.Feature{mkFeature("g1", "", "", model.Gene)}, ApplyOptions{}); probs != nil {
		t.Fatal(probs)
	}
	if _, probs := c.Apply([]model.Feature{
		mkFeature("g1", "", "", model.Gene),
	}, ApplyOptions{OnConflict: ConflictOverwrite}); probs != nil {
		t.Fatal(probs)
	}
	if c.Revision() != 2 {
		t.Fatalf("revision = %d", c.Revision())
	}
}

func TestVersionEvolutionAndRegression(t *testing.T) {
	t.Log("业务: 版本演进 — 新版本保留历史, 版本回退被拒")
	c := NewCatalog()
	batch := func(version string, probsWant string) {
		f := mkFeature("g1", "", version, model.Gene)
		_, probs := c.Apply([]model.Feature{f}, ApplyOptions{})
		if probsWant == "" {
			if probs != nil {
				t.Fatalf("v%s unexpected: %v", version, probs)
			}
			return
		}
		if probs == nil || probs.Codes()[probsWant] != 1 {
			t.Fatalf("v%s want %s, got %v", version, probsWant, probs)
		}
	}
	batch("1", "")
	batch("2", "")
	batch("2", ProbDuplicateVersion)

	snap := c.Snapshot()
	vs := snap.Features["g1"]
	if len(vs) != 2 || !vs[len(vs)-1].Current || vs[len(vs)-1].Feature.Version != "2" {
		t.Fatalf("version chain wrong: %+v", vs)
	}
}

func TestDottedVersionEvolution(t *testing.T) {
	t.Log("业务: 版本管理 — 点分版本 1.2 之后 1.10 为升级、1.9 为回退")
	c := NewCatalog()
	for _, v := range []string{"1", "1.2", "1.10"} {
		if _, probs := c.Apply([]model.Feature{mkFeature("g1", "", v, model.Gene)}, ApplyOptions{}); probs != nil {
			t.Fatalf("v%s: %v", v, probs)
		}
	}
	_, probs := c.Apply([]model.Feature{mkFeature("g1", "", "1.9", model.Gene)}, ApplyOptions{})
	if probs == nil || probs.Codes()[ProbVersionRegression] != 1 {
		t.Fatalf("want regression, got %v", probs)
	}
	cur, _ := c.Snapshot().Current("g1")
	if cur.Version != "1.10" {
		t.Fatalf("current = %s", cur.Version)
	}
}

func TestDanglingParent(t *testing.T) {
	t.Log("业务: 悬空引用 — 父id不存在整批拒绝")
	c := NewCatalog()
	_, probs := c.Apply([]model.Feature{
		mkFeature("e1", "missing", "", model.Exon),
	}, ApplyOptions{})
	if probs == nil || probs.Codes()[ProbDanglingParent] != 1 {
		t.Fatalf("want dangling, got %v", probs)
	}
	if c.Revision() != 0 {
		t.Fatal("revision must stay 0 after failed batch")
	}
}

func TestCycleAndSelfReference(t *testing.T) {
	t.Log("业务: 环形引用 — 自环与多节点环检测")
	c := NewCatalog()
	_, probs := c.Apply([]model.Feature{
		mkFeature("a", "b", "", model.Other),
		mkFeature("b", "a", "", model.Other),
	}, ApplyOptions{})
	if probs == nil || probs.Codes()[ProbCycle] == 0 {
		t.Fatalf("want cycle, got %v", probs)
	}

	c2 := NewCatalog()
	_, probs = c2.Apply([]model.Feature{
		mkFeature("self", "self", "", model.Other),
	}, ApplyOptions{})
	if probs == nil || probs.Codes()[ProbSelfReference] != 1 {
		t.Fatalf("want self reference, got %v", probs)
	}
}

func TestBadParentType(t *testing.T) {
	t.Log("业务: 分层关系 — 非法父子类型拒绝")
	c := NewCatalog()
	_, probs := c.Apply([]model.Feature{
		mkFeature("t1", "", "", model.Transcript),
		mkFeature("g1", "t1", "", model.Gene), // gene parented by transcript
	}, ApplyOptions{})
	if probs == nil || probs.Codes()[ProbBadParentType] != 1 {
		t.Fatalf("want bad_parent_type, got %v", probs)
	}
}

func TestChildrenAndAncestors(t *testing.T) {
	t.Log("业务: 分层关系 — 合法三级关系与祖先链")
	c := NewCatalog()
	_, probs := c.Apply([]model.Feature{
		mkFeature("g1", "", "", model.Gene),
		mkFeature("t1", "g1", "", model.Transcript),
		mkFeature("e1", "t1", "", model.Exon),
	}, ApplyOptions{})
	if probs != nil {
		t.Fatal(probs)
	}
	snap := c.Snapshot()
	if len(snap.Children["g1"]) != 1 || snap.Children["g1"][0] != "t1" {
		t.Fatalf("children wrong: %v", snap.Children)
	}
	anc := snap.Ancestors("e1")
	if len(anc) != 2 || anc[0].ID != "t1" || anc[1].ID != "g1" {
		t.Fatalf("ancestor chain wrong: %v", anc)
	}
}

func TestAtomicBatchIsolation(t *testing.T) {
	t.Log("业务: 原子性 — 同批中一条非法则整批不可见")
	c := NewCatalog()
	_, probs := c.Apply([]model.Feature{
		mkFeature("ok", "", "", model.Gene),
		mkFeature("bad", "ghost", "", model.Exon),
	}, ApplyOptions{})
	if probs == nil {
		t.Fatal("want problems")
	}
	if _, ok := c.Snapshot().Current("ok"); ok {
		t.Fatal("rejected batch leaked a feature")
	}
}
