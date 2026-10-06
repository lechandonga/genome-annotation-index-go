package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

func tmpStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "idx")
	st, err := Open(dir, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return st, dir
}

func feat(id, seq string, start, end int, strand model.Strand, ftype model.FeatureType, parents ...string) model.Feature {
	iv, err := coord.NewInterval(start, end)
	if err != nil {
		panic(err)
	}
	return model.Feature{
		ID: id, Type: ftype, SeqID: seq, Strand: strand,
		Interval: iv, ParentIDs: parents,
		Source: model.Source{Name: "test", Format: "gff3", File: "x.gff3"},
	}
}

func ids(refs []model.FeatureRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

func seedGeneFamily(t *testing.T, st *Store) {
	t.Helper()
	batch := []model.Feature{
		feat("g1", "chr1", 100, 500, model.Forward, model.Gene),
		feat("t1", "chr1", 120, 480, model.Forward, model.Transcript, "g1"),
		feat("e1", "chr1", 120, 200, model.Forward, model.Exon, "t1"),
		feat("e2", "chr1", 300, 480, model.Forward, model.Exon, "t1"),
		feat("g2", "chr1", 600, 800, model.Reverse, model.Gene),
		feat("g3", "chr2", 0, 50, model.Unstranded, model.Gene),
	}
	if _, err := st.Commit(batch, CommitOptions{Source: "seed"}); err != nil {
		t.Fatal(err)
	}
}

func TestIntervalBoundaryOverlap(t *testing.T) {
	t.Log("业务: 区间查询边界 — 半开相接不算重叠, 单碱基重叠命中")
	st, _ := tmpStore(t)
	defer st.Close()
	seedGeneFamily(t, st)

	if got := st.Query(Query{SeqID: "chr1", Region: coord.Interval{Start: 0, End: 100}}); len(got) != 0 {
		t.Fatalf("touching at start must not overlap, got %v", ids(got))
	}
	if got := st.Query(Query{SeqID: "chr1", Region: coord.Interval{Start: 500, End: 600}}); len(got) != 0 {
		t.Fatalf("touching at end must not overlap, got %v", ids(got))
	}
	got := st.Query(Query{SeqID: "chr1", Region: coord.Interval{Start: 99, End: 101}})
	if len(got) != 1 || got[0].ID != "g1" {
		t.Fatalf("one-base overlap must hit g1, got %v", ids(got))
	}
}

func TestStrandFilters(t *testing.T) {
	t.Log("业务: 链方向过滤 — +/-/无链 包含与严格模式")
	st, _ := tmpStore(t)
	defer st.Close()
	seedGeneFamily(t, st)

	region := coord.Interval{Start: 0, End: 1000}
	all := st.Query(Query{SeqID: "chr1", Region: region, Strand: StrandAny})
	if len(all) != 5 {
		t.Fatalf("any strand want 5 hits, got %v", ids(all))
	}
	fwd := st.Query(Query{SeqID: "chr1", Region: region, Strand: StrandForward})
	if len(fwd) != 4 {
		t.Fatalf("forward(+unstranded) want 4, got %v", ids(fwd))
	}
	revOnly := st.Query(Query{SeqID: "chr1", Region: region, Strand: StrandReverseOnly})
	if len(revOnly) != 1 || revOnly[0].ID != "g2" {
		t.Fatalf("reverse-only want g2, got %v", ids(revOnly))
	}
	fwdOnly := st.Query(Query{SeqID: "chr1", Region: region, Strand: StrandForwardOnly})
	if len(fwdOnly) != 4 {
		t.Fatalf("forward-only want 4, got %v", ids(fwdOnly))
	}
	for _, r := range fwdOnly {
		if r.Strand != model.Forward {
			t.Fatalf("strict filter leaked strand %v", r.Strand)
		}
	}
	inc := st.Query(Query{SeqID: "chr2", Region: region, Strand: StrandReverse})
	if len(inc) != 1 {
		t.Fatalf("unstranded should match inclusive reverse, got %v", ids(inc))
	}
	strict := st.Query(Query{SeqID: "chr2", Region: region, Strand: StrandReverseOnly})
	if len(strict) != 0 {
		t.Fatalf("unstranded must not match strict reverse, got %v", ids(strict))
	}
}

func TestTypeAndLineage(t *testing.T) {
	t.Log("业务: 类型过滤与祖先链扩展")
	st, _ := tmpStore(t)
	defer st.Close()
	seedGeneFamily(t, st)

	exonsOnly := st.Query(Query{
		SeqID:  "chr1",
		Region: coord.Interval{Start: 0, End: 1000},
		Types:  []model.FeatureType{model.Exon},
	})
	if len(exonsOnly) != 2 {
		t.Fatalf("want 2 exons, got %v", ids(exonsOnly))
	}

	withAncestors := st.Query(Query{
		SeqID:          "chr1",
		Region:         coord.Interval{Start: 120, End: 130},
		IncludeLineage: true,
	})
	gotIDs := map[string]bool{}
	for _, r := range withAncestors {
		gotIDs[r.ID] = true
	}
	for _, want := range []string{"e1", "t1", "g1"} {
		if !gotIDs[want] {
			t.Fatalf("lineage query missing %s, got %v", want, ids(withAncestors))
		}
	}
}

func TestPersistenceAndReopen(t *testing.T) {
	t.Log("业务: 持久化与重启 — 重新打开后查询结论一致")
	st, dir := tmpStore(t)
	seedGeneFamily(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(dir, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if st2.Revision() != 1 {
		t.Fatalf("revision after reopen = %d", st2.Revision())
	}
	got := st2.Query(Query{SeqID: "chr1", Region: coord.Interval{Start: 0, End: 1000}})
	if len(got) != 5 {
		t.Fatalf("after reopen want 5 hits, got %v", ids(got))
	}
}

func TestTruncatedEventTailRecovery(t *testing.T) {
	t.Log("业务: 崩溃恢复 — 末尾截断事件被安全丢弃")
	st, dir := tmpStore(t)
	seedGeneFamily(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "events.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, append(data, []byte("{\"type\":\"commi")...), 0o644); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(dir, OpenOptions{})
	if err != nil {
		t.Fatalf("truncated tail must recover, got %v", err)
	}
	defer st2.Close()
	if st2.Revision() != 1 {
		t.Fatalf("revision after recovery = %d", st2.Revision())
	}
}

func TestCorruptEventLogFailsExplicitly(t *testing.T) {
	t.Log("业务: 崩溃恢复 — 完整事件校验和损坏必须明确失败")
	st, dir := tmpStore(t)
	seedGeneFamily(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "events.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	pos := len(data) / 2
	data[pos] ^= 0xFF
	if err := os.WriteFile(logPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, OpenOptions{}); !IsCorrupt(err) {
		t.Fatalf("want CorruptError, got %v", err)
	}
}

func TestCorruptSnapshotRegenerated(t *testing.T) {
	t.Log("业务: 崩溃恢复 — 快照/清单损坏时从事件日志安全重建")
	st, dir := tmpStore(t)
	seedGeneFamily(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "snapshot.json")); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(dir, OpenOptions{})
	if err != nil {
		t.Fatalf("missing snapshot must be regenerated: %v", err)
	}
	defer st2.Close()
	if len(st2.Query(Query{SeqID: "chr1", Region: coord.Interval{Start: 0, End: 1000}})) != 5 {
		t.Fatal("regenerated snapshot must answer correctly")
	}
}

func TestRebuildIdempotent(t *testing.T) {
	t.Log("业务: 索引重建 — 显式重建结果一致且版本号相同")
	st, _ := tmpStore(t)
	defer st.Close()
	seedGeneFamily(t, st)
	rev, err := st.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	if rev != 1 {
		t.Fatalf("rebuilt revision = %d", rev)
	}
	if len(st.Query(Query{SeqID: "chr1", Region: coord.Interval{Start: 0, End: 1000}})) != 5 {
		t.Fatal("rebuilt index must answer identically")
	}
}
