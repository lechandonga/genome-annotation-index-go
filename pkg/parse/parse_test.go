package parse_test

import (
	"strings"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/parse"
	"github.com/lechandonga/genome-annotation-index-go/pkg/testutil"
)

func opts(fmt_ parse.Format) parse.Options {
	return parse.Options{Format: fmt_, URI: "file:///x/test." + string(fmt_), Source: "src"}
}

func TestGFF3Business(t *testing.T) {
	const gff = `##gff-version 3
chr1	test	gene	1	100	.	+	.	ID=g1
chr1	test	mRNA	1	100	.	+	.	ID=t1;Parent=g1
chr1	test	exon	1	50	.	+	.	Parent=t1
chr1	test	exon	51	100	.	-	.	Parent=t1
`
	testutil.RunGroup(t, "parse.gff3", []testutil.Case{
		{
			Name: "1-based closed -> 0-based half-open conversion",
			Run: func() string {
				r := parse.Parse(strings.NewReader(gff), opts(parse.FormatGFF3))
				if len(r.Rejections) != 0 {
					return testutil.Fail("unexpected rejections: %v", r.Rejections)
				}
				if r.Features[0].Start != 0 || r.Features[0].End != 100 {
					return testutil.Fail("gene coords = [%d,%d)", r.Features[0].Start, r.Features[0].End)
				}
				if r.Features[2].Start != 0 || r.Features[2].End != 50 ||
					r.Features[3].Start != 50 || r.Features[3].End != 100 {
					return testutil.Fail("exon conversion wrong: %+v %+v", r.Features[2], r.Features[3])
				}
				return ""
			},
		},
		{
			Name: "derived id for ID-less exon under one parent",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\texon\t5\t9\t.\t+\t.\tParent=t1\n"), opts(parse.FormatGFF3))
				if len(r.Rejections) != 0 || len(r.Features) != 1 {
					return testutil.Fail("rej=%v feats=%d", r.Rejections, len(r.Features))
				}
				if !strings.HasPrefix(r.Features[0].ID, "exon:t1:4-9") {
					return testutil.Fail("id=%q", r.Features[0].ID)
				}
				return ""
			},
		},
		{
			Name: "wrong column count -> malformed_record",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\tgene\t1\t10\n"), opts(parse.FormatGFF3))
				if !hasKind(r.Rejections, model.RejectMalformed) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "non-numeric coordinate -> bad_coordinate",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\tgene\tx\t10\t.\t+\t.\tID=g\n"), opts(parse.FormatGFF3))
				if !hasKind(r.Rejections, model.RejectBadCoordinate) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "zero/negative 1-based coordinate -> bad_coordinate",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\tgene\t0\t10\t.\t+\t.\tID=g\n"), opts(parse.FormatGFF3))
				if !hasKind(r.Rejections, model.RejectBadCoordinate) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "bad strand token -> bad_strand",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\tgene\t1\t10\t.\tX\t.\tID=g\n"), opts(parse.FormatGFF3))
				if !hasKind(r.Rejections, model.RejectBadStrand) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "missing ID -> missing_id",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\tgene\t1\t10\t.\t+\t.\tnote=hello\n"), opts(parse.FormatGFF3))
				if !hasKind(r.Rejections, model.RejectMissingID) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "malformed attributes -> bad_attribute",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\tgene\t1\t10\t.\t+\t.\tIDg1\n"), opts(parse.FormatGFF3))
				if !hasKind(r.Rejections, model.RejectBadAttribute) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
	})
}

func TestGTFBEDBusiness(t *testing.T) {
	const gtf = `chr1	test	gene	1	100	.	+	.	gene_id "G1";
chr1	test	transcript	1	100	.	+	.	gene_id "G1"; transcript_id "T1";
chr1	test	exon	1	50	.	+	.	gene_id "G1"; transcript_id "T1";
`
	testutil.RunGroup(t, "parse.gtf", []testutil.Case{
		{
			Name: "coords converted and synthetic gene + hierarchy",
			Run: func() string {
				r := parse.Parse(strings.NewReader(gtf), opts(parse.FormatGTF))
				if len(r.Rejections) != 0 {
					return testutil.Fail("rej=%v", r.Rejections)
				}
				var gene, tx, exon model.Feature
				for _, f := range r.Features {
					switch f.Type {
					case model.FeatureGene:
						gene = f
					case model.FeatureTranscript:
						tx = f
					case model.FeatureExon:
						exon = f
					}
				}
				if gene.ID != "G1" || tx.Parents[0] != "G1" || exon.Parents[0] != "T1" {
					return testutil.Fail("hierarchy wrong: %+v %+v %+v", gene, tx, exon)
				}
				if exon.Start != 0 || exon.End != 50 {
					return testutil.Fail("exon coords %d-%d", exon.Start, exon.End)
				}
				return ""
			},
		},
		{
			Name: "missing transcript_id on exon -> missing_id",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\tt\texon\t1\t9\t.\t+\t.\tgene_id \"G\";\n"), opts(parse.FormatGTF))
				if !hasKind(r.Rejections, model.RejectMissingID) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
	})

	testutil.RunGroup(t, "parse.bed", []testutil.Case{
		{
			Name: "BED6 kept 0-based half-open",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\t0\t100\tpeak1\t0\t-\n"), opts(parse.FormatBED))
				if len(r.Rejections) != 0 || r.Features[0].Start != 0 || r.Features[0].End != 100 ||
					r.Features[0].Strand != "-" {
					return testutil.Fail("rej=%v f=%+v", r.Rejections, r.Features)
				}
				return ""
			},
		},
		{
			Name: "BED12 expands transcript + exons",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\t0\t100\ttx1\t0\t+\t0\t100\t0\t2\t20,30,\t0,50,\n"), opts(parse.FormatBED))
				if len(r.Rejections) != 0 {
					return testutil.Fail("rej=%v", r.Rejections)
				}
				if len(r.Features) != 3 || r.Features[0].Type != model.FeatureTranscript {
					return testutil.Fail("features=%d", len(r.Features))
				}
				if r.Features[1].Start != 0 || r.Features[1].End != 20 ||
					r.Features[2].Start != 50 || r.Features[2].End != 80 {
					return testutil.Fail("block coords wrong: %+v %+v", r.Features[1], r.Features[2])
				}
				return ""
			},
		},
		{
			Name: "block past transcript end -> bad_coordinate",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\t0\t100\ttx1\t0\t+\t0\t100\t0\t1\t90,\t20,\n"), opts(parse.FormatBED))
				if !hasKind(r.Rejections, model.RejectBadCoordinate) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "BED without name -> missing_id",
			Run: func() string {
				r := parse.Parse(strings.NewReader("chr1\t0\t100\n"), opts(parse.FormatBED))
				if !hasKind(r.Rejections, model.RejectMissingID) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
	})
}

func TestFASTABusiness(t *testing.T) {
	testutil.RunGroup(t, "parse.fasta", []testutil.Case{
		{
			Name: "id, length and checksum recorded",
			Run: func() string {
				r := parse.Parse(strings.NewReader(">chr1\nACGT\nacgt\n"), opts(parse.FormatFASTA))
				if len(r.Rejections) != 0 || len(r.Sequences) != 1 {
					return testutil.Fail("rej=%v seqs=%d", r.Rejections, len(r.Sequences))
				}
				if r.Sequences[0].ID != "chr1" || r.Sequences[0].Length != 8 {
					return testutil.Fail("seq=%+v", r.Sequences[0])
				}
				if r.Sequences[0].SHA256 == "" {
					return "empty checksum"
				}
				return ""
			},
		},
		{
			Name: "data before header -> malformed_record",
			Run: func() string {
				r := parse.Parse(strings.NewReader("ACGT\n>c\nA\n"), opts(parse.FormatFASTA))
				if !hasKind(r.Rejections, model.RejectMalformed) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
		{
			Name: "illegal base characters -> malformed_record",
			Run: func() string {
				r := parse.Parse(strings.NewReader(">c\nACGTXYZ@@\n"), opts(parse.FormatFASTA))
				if !hasKind(r.Rejections, model.RejectMalformed) {
					return testutil.Fail("got %v", kinds(r.Rejections))
				}
				return ""
			},
		},
	})
}

func kinds(rs []model.Rejection) []model.RejectKind {
	var out []model.RejectKind
	for _, r := range rs {
		out = append(out, r.Kind)
	}
	return out
}

func hasKind(rs []model.Rejection, k model.RejectKind) bool {
	for _, r := range rs {
		if r.Kind == k {
			return true
		}
	}
	return false
}
