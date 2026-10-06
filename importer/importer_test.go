package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/store"
)

func TestEndToEndMixedFormats(t *testing.T) {
	t.Log("业务: 端到端 — GFF3+GTF+BED 混合导入, 统一坐标下区间查询")
	dir := filepath.Join(t.TempDir(), "idx")
	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	files := []File{
		{Path: "../testdata/demo.gff3", SourceName: "src-gff3"},
		{Path: "../testdata/demo.gtf", SourceName: "src-gtf"},
		{Path: "../testdata/demo.bed", SourceName: "src-bed"},
	}
	rep, err := Import(st, files, "", graph.ConflictReject)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rejected) != 0 {
		t.Fatalf("unexpected rejects: %v", rep.Rejected)
	}
	if rep.Committed == nil {
		t.Fatal("expected commit")
	}

	// GFF3 gene1 [0,1000) and BED txBed1 [99,400) coexist and are both found.
	refs := st.Query(store.Query{SeqID: "chr1", Region: coord.Interval{Start: 0, End: 50}})
	ids := map[string]bool{}
	for _, r := range refs {
		ids[r.ID] = true
	}
	if !ids["gene1"] {
		t.Fatalf("expected gff3 gene1 in hits: %v", ids)
	}
}

func TestMalformedFileDoesNotCommit(t *testing.T) {
	t.Log("业务: 端到端 — 含畸形记录的文件整批拒绝, 索引保持空视图")
	dir := filepath.Join(t.TempDir(), "idx")
	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rep, err := Import(st, []File{{Path: "../testdata/malformed.gff3"}}, "", graph.ConflictReject)
	if err == nil {
		t.Fatal("malformed import must return an error")
	}
	if rep == nil || len(rep.Rejected) == 0 {
		t.Fatal("rejection report must explain reasons")
	}
	reasons := rep.Rejected.Reasons()
	if reasons["bad_coordinate_system"] == 0 || reasons["bad_strand"] == 0 {
		t.Fatalf("distinct reasons missing in report: %v", reasons)
	}
	t.Logf("  端到端拒绝原因分布: %v", reasons)
	if st.Revision() != 0 {
		t.Fatalf("no commit allowed, revision=%d", st.Revision())
	}
	if len(st.Snapshot().Features) != 0 {
		t.Fatal("store must remain empty after rejected import")
	}
}

func TestReferenceBoundRejection(t *testing.T) {
	t.Log("业务: 端到端 — 参考序列存在时越界记录拒绝")
	dir := filepath.Join(t.TempDir(), "idx")
	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// demo.gff3 coordinates exceed the tiny reference; import must fail with
	// out_of_range/unknown_reference rejects.
	rep, err := Import(st, []File{{Path: "../testdata/demo.gff3"}}, "../testdata/ref.fa", graph.ConflictReject)
	if err == nil {
		t.Fatal("expected bound rejection")
	}
	if rep == nil {
		t.Fatal("expected report")
	}
	reasons := rep.Rejected.Reasons()
	if reasons["out_of_range"] == 0 && reasons["unknown_reference"] == 0 {
		t.Fatalf("expected coordinate rejections, got %v", reasons)
	}
}

func TestCLIHelp(t *testing.T) {
	// Smoke test that testdata files still exist for reproducible local runs.
	for _, p := range []string{"../testdata/demo.gff3", "../testdata/demo.gtf", "../testdata/demo.bed", "../testdata/ref.fa"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing testdata %s: %v", p, err)
		}
	}
}
