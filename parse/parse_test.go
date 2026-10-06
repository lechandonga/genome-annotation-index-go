package parse

import (
	"os"
	"strings"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/model"
)

// businessLog prints a grouped test header so `go test -v` output shows
// which business area each case covers.

func TestGFF3HappyPathAndCoordinates(t *testing.T) {
	t.Log("业务: 多格式解析与坐标归一 — GFF3 1-based闭区间转0-based半开")
	f, err := os.Open("../testdata/demo.gff3")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	feats, rejects, err := ParseGFF3(f, "demo.gff3", Options{SourceName: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejects) != 0 {
		t.Fatalf("unexpected rejects: %v", rejects)
	}
	if len(feats) != 5 {
		t.Fatalf("want 5 features, got %d", len(feats))
	}
	gene1 := findID(feats, "gene1")
	if gene1.Interval.Start != 0 || gene1.Interval.End != 1000 {
		t.Fatalf("gene1 coords = [%d,%d)", gene1.Interval.Start, gene1.Interval.End)
	}
	if gene1.Strand != model.Forward || gene1.Type != model.Gene {
		t.Fatalf("gene1 type/strand wrong: %v %v", gene1.Type, gene1.Strand)
	}
	exon := findID(feats, "exon1a")
	if exon.Interval.Start != 100 || exon.Interval.End != 300 {
		t.Fatalf("exon1a coords = [%d,%d)", exon.Interval.Start, exon.Interval.End)
	}
	if len(exon.ParentIDs) != 1 || exon.ParentIDs[0] != "tx1" {
		t.Fatalf("exon parent wrong: %v", exon.ParentIDs)
	}
	if gene2 := findID(feats, "gene2"); gene2.Version != "1" || gene2.Strand != model.Reverse {
		t.Fatalf("gene2 version/strand wrong: %q %v", gene2.Version, gene2.Strand)
	}
}

func TestGFF3MalformedReasons(t *testing.T) {
	t.Log("业务: 畸形输入拒绝原因可区分 — 坐标/数字/链/语法/悬空")
	feats, rejects, err := ParseGFF3(strings.NewReader(gffMalformed), "m.gff3", Options{})
	if err != nil {
		t.Fatal(err)
	}
	counts := rejects.Reasons()
	want := map[Reason]int{
		ReasonBadCoordinateSystem: 2, // zeroStart + inverted
		ReasonBadNumber:           1,
		ReasonBadStrand:           1,
		ReasonSyntax:              2, // bad columns + bad attribute
	}
	for r, n := range want {
		if counts[r] != n {
			t.Fatalf("reason %s: got %d, want %d (all=%v)", r, counts[r], n, counts)
		}
	}
	if len(feats) != 2 {
		t.Fatalf("ok1 and orphan parse; orphan is rejected at graph layer; got %d", len(feats))
	}
	for _, r := range rejects {
		t.Logf("  拒绝用例 %s:%d [%s] %s", r.File, r.Line, r.Reason, r.Message)
	}
}

const gffMalformed = "##gff-version 3\n" +
	"chr1\tt\tgene\t1\t1000\t.\t+\t.\tID=ok1\n" +
	"chr1\tt\tgene\t0\t10\t.\t+\t.\tID=zeroStart\n" +
	"chr1\tt\tgene\t5\t4\t.\t+\t.\tID=inverted\n" +
	"chr1\tt\tgene\tabc\t10\t.\t+\t.\tID=badNumber\n" +
	"chr1\tt\tgene\t1\t10\t.\tX\t.\tID=badStrand\n" +
	"badcols\tline\n" +
	"chr1\tt\tgene\t1\t10\t.\t+\t.\tID=badA;notkv\n" +
	"chr1\tt\texon\t1\t10\t.\t+\t.\tID=orphan;Parent=missingParent\n"

func TestGTFHierarchyAndIdentity(t *testing.T) {
	t.Log("业务: 多格式解析 — GTF gene_id/transcript_id 父子关系与确定性exon id")
	f, err := os.Open("../testdata/demo.gtf")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	feats, rejects, err := ParseGTF(f, "demo.gtf", Options{SourceName: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejects) != 0 {
		t.Fatalf("rejects: %v", rejects)
	}
	if len(feats) != 5 {
		t.Fatalf("want 5 rows, got %d", len(feats))
	}
	var tx *model.Feature
	var exons []model.Feature
	for i := range feats {
		if feats[i].ID == "tA1" {
			tx = &feats[i]
		}
		if feats[i].Type == model.Exon {
			exons = append(exons, feats[i])
		}
	}
	if tx == nil || len(tx.ParentIDs) != 1 || tx.ParentIDs[0] != "gA" {
		t.Fatalf("transcript parent wrong: %+v", tx)
	}
	if len(exons) != 2 || exons[0].ID == exons[1].ID {
		t.Fatalf("two distinct deterministic exon ids required, got %+v", exons)
	}
	for _, e := range exons {
		if len(e.ParentIDs) != 1 || e.ParentIDs[0] != "tA1" {
			t.Fatalf("exon parent wrong: %+v", e)
		}
	}
}

func TestGTFMissingIDs(t *testing.T) {
	t.Log("业务: 畸形输入 — GTF 缺少 gene_id/transcript_id")
	in := "chr1\tt\ttranscript\t1\t10\t.\t+\t.\tgene_id \"g1\";\n" +
		"chr1\tt\texon\t1\t10\t.\t+\t.\ttranscript_id \"t1\";\n"
	_, rejects, err := ParseGTF(strings.NewReader(in), "x.gtf", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejects) != 2 {
		t.Fatalf("want 2 rejects, got %d (%v)", len(rejects), rejects)
	}
	for _, r := range rejects {
		if r.Reason != ReasonMissingField {
			t.Fatalf("want missing_field, got %s", r.Reason)
		}
	}
}

func TestBEDCoordinatesAndBlocks(t *testing.T) {
	t.Log("业务: 多格式解析 — BED 0-based半开与BED12外显子块")
	f, err := os.Open("../testdata/demo.bed")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	feats, rejects, err := ParseBED(f, "demo.bed", Options{SourceName: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejects) != 0 {
		t.Fatalf("rejects: %v", rejects)
	}
	// txBed1 + 2 exons, txBed2, synthetic name row = 5
	if len(feats) != 5 {
		t.Fatalf("want 5 features, got %d", len(feats))
	}
	tx := findID(feats, "txBed1")
	if tx.Interval.Start != 99 || tx.Interval.End != 400 {
		t.Fatalf("txBed1 coords wrong: %+v", tx.Interval)
	}
	e1 := findID(feats, "txBed1.exon1")
	if e1.Interval.Start != 99 || e1.Interval.End != 299 {
		t.Fatalf("block1 should be [99,299), got [%d,%d)", e1.Interval.Start, e1.Interval.End)
	}
	e2 := findID(feats, "txBed1.exon2")
	if e2.Interval.Start != 299 || e2.Interval.End != 399 {
		t.Fatalf("block2 should be [299,399), got [%d,%d)", e2.Interval.Start, e2.Interval.End)
	}
}

func TestBEDBlockValidation(t *testing.T) {
	t.Log("业务: 畸形输入 — BED块越界/数量不一致")
	cases := map[string]string{
		"count_mismatch": "chr1\t0\t100\tx\t0\t+\t0\t0\t0\t2\t10,20,\t0,\n",
		"block_past_end": "chr1\t0\t100\tx\t0\t+\t0\t0\t0\t1\t50,\t60,\n",
		"bad_count":      "chr1\t0\t100\tx\t0\t+\t0\t0\t0\t0\t10,\t0,\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, rejects, err := ParseBED(strings.NewReader(in), "x.bed", Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(rejects) != 1 {
				t.Fatalf("want 1 reject, got %d (%v)", len(rejects), rejects)
			}
			if rejects[0].Reason != ReasonBlockInvalid && rejects[0].Reason != ReasonOutOfRange {
				t.Fatalf("unexpected reason %s", rejects[0].Reason)
			}
		})
	}
}

func TestReferenceBoundsAndUnknownSeq(t *testing.T) {
	t.Log("业务: 坐标越界 — FASTA参考校验未知序列与超长")
	f, _ := os.Open("../testdata/ref.fa")
	defer f.Close()
	seqs, _, err := ParseFASTA(f, "ref.fa")
	if err != nil {
		t.Fatal(err)
	}
	if seqs["chr1"] != 20 || seqs["chr2"] != 15 {
		t.Fatalf("lengths wrong: %+v", seqs)
	}
	in := "##gff-version 3\n" +
		"chr1\tt\tgene\t1\t20\t.\t+\t.\tID=fits\n" +
		"chr1\tt\tgene\t1\t21\t.\t+\t.\tID=tooLong\n" +
		"chr9\tt\tgene\t1\t5\t.\t+\t.\tID=unknownSeq\n"
	_, rejects, err := ParseGFF3(strings.NewReader(in), "x.gff3", Options{Sequences: seqs})
	if err != nil {
		t.Fatal(err)
	}
	counts := rejects.Reasons()
	if counts[ReasonOutOfRange] != 1 || counts[ReasonUnknownSeqID] != 1 {
		t.Fatalf("reject counts wrong: %v", counts)
	}
}

func TestEmptyFeatureRejected(t *testing.T) {
	t.Log("业务: 畸形输入 — BED零长度区间拒绝")
	_, rejects, err := ParseBED(strings.NewReader("chr1\t10\t10\tx\n"), "z.bed", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejects) != 1 || rejects[0].Reason != ReasonEmptyFeature {
		t.Fatalf("want one empty_feature reject, got %v", rejects)
	}
}

func TestFASTADuplicateAndBadBase(t *testing.T) {
	t.Log("业务: 畸形输入 — FASTA重复id与非法碱基")
	if _, _, err := ParseFASTA(strings.NewReader(">a\nACGT\n>a\nACGT\n"), "x.fa"); err == nil {
		t.Fatal("duplicate id must error")
	}
	if _, _, err := ParseFASTA(strings.NewReader(">a\nAC!GT\n"), "x.fa"); err == nil {
		t.Fatal("invalid base must error")
	}
	if _, _, err := ParseFASTA(strings.NewReader("ACGT\n"), "x.fa"); err == nil {
		t.Fatal("data before header must error")
	}
}

func TestSniff(t *testing.T) {
	t.Log("业务: 多格式解析 — 内容与扩展名嗅探")
	cases := map[string]string{
		"##gff-version 3\n":      FormatGFF3,
		"x.gtf":                  FormatGTF,
		"chr1\t0\t10\ta\t0\t+\n": FormatBED,
		"x.bed":                  FormatBED,
	}
	for content, want := range cases {
		name := "x.dat"
		if content == "x.gtf" || content == "x.bed" {
			name = content
		}
		got, _, err := SniffFormat(strings.NewReader(content), name)
		if err != nil {
			t.Fatalf("sniff %q: %v", content, err)
		}
		if got != want {
			t.Fatalf("sniff %q = %s, want %s", content, got, want)
		}
	}
}

func findID(fs []model.Feature, id string) model.Feature {
	for _, f := range fs {
		if f.ID == id {
			return f
		}
	}
	return model.Feature{}
}
