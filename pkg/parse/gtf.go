package parse

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// ParseGTF parses GTF (GFF2 style, 9 columns, 1-based closed coords).
func ParseGTF(r io.Reader, opts Options) Result {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	// GTF files commonly omit explicit gene lines: remember synthetic gene
	// features seen via gene_id so each is emitted exactly once.
	genes := map[string]bool{}
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f, geneID, geneSeq, rej := parseGTFLine(line, opts, lineNo)
		if rej != nil {
			res.Rejections = append(res.Rejections, *rej)
			continue
		}
		if geneID != "" && !genes[geneID] {
			genes[geneID] = true
			res.Features = append(res.Features, model.Feature{
				ID: geneID, Version: 1, Type: model.FeatureGene,
				SeqID: geneSeq, Start: 0, End: 0, Strand: f.Strand,
				Source: opts.Source, URI: opts.URI, Attrs: map[string]string{"synthetic": "true"},
			})
		}
		res.Features = append(res.Features, f)
	}
	if err := sc.Err(); err != nil {
		res.Rejections = append(res.Rejections, model.Rejection{
			Kind: model.RejectMalformed, URI: opts.URI, Line: lineNo, Detail: "read error: " + err.Error(),
		})
	}
	return res
}

var gtfTypeMap = map[string]model.FeatureType{
	"gene":        model.FeatureGene,
	"mRNA":        model.FeatureTranscript,
	"transcript":  model.FeatureTranscript,
	"exon":        model.FeatureExon,
	"CDS":         model.FeatureCDS,
	"start_codon": model.FeatureOther,
	"stop_codon":  model.FeatureOther,
	"UTR":         model.FeatureOther,
}

func parseGTFLine(line string, opts Options, lineNo int) (f model.Feature, geneID, geneSeq string, rej *model.Rejection) {
	cols := strings.Split(line, "\t")
	if len(cols) != 9 {
		return f, "", "", ptrRej(model.RejectMalformed, opts.URI, lineNo, "expected 9 tab-separated columns, got %d", len(cols))
	}
	for i, c := range cols[:8] {
		if strings.TrimSpace(c) == "" {
			return f, "", "", ptrRej(model.RejectMissingField, opts.URI, lineNo, "column %d is empty", i+1)
		}
	}
	rawType := cols[2]
	ftype, ok := gtfTypeMap[rawType]
	if !ok {
		ftype = model.FeatureOther
	}
	start1, err := strconv.Atoi(cols[3])
	if err != nil {
		return f, "", "", ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "start not an integer: %q", cols[3])
	}
	end1, err := strconv.Atoi(cols[4])
	if err != nil {
		return f, "", "", ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "end not an integer: %q", cols[4])
	}
	if start1 < 1 || end1 < 1 || end1 < start1 {
		return f, "", "", ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "invalid 1-based closed interval %d-%d", start1, end1)
	}
	strand := cols[6]
	if strand != model.StrandPlus && strand != model.StrandMinus && strand != model.StrandUnknown {
		return f, "", "", ptrRej(model.RejectBadStrand, opts.URI, lineNo, "strand %q not in + - .", strand)
	}
	attrs, err := parseGTFAttributes(cols[8])
	if err != nil {
		return f, "", "", ptrRej(model.RejectBadAttribute, opts.URI, lineNo, "%v", err)
	}
	gid := attrs["gene_id"]
	tid := attrs["transcript_id"]
	var id string
	var parents []string
	switch ftype {
	case model.FeatureGene:
		if gid == "" {
			return f, "", "", ptrRej(model.RejectMissingID, opts.URI, lineNo, "gene feature requires gene_id")
		}
		id = gid
	case model.FeatureTranscript:
		if tid == "" {
			return f, "", "", ptrRej(model.RejectMissingID, opts.URI, lineNo, "transcript feature requires transcript_id")
		}
		id = tid
		if gid != "" {
			parents = []string{gid}
		}
	case model.FeatureExon, model.FeatureCDS:
		if tid == "" {
			return f, "", "", ptrRej(model.RejectMissingID, opts.URI, lineNo, "%s feature requires transcript_id", rawType)
		}
		id = fmt.Sprintf("%s:%s:%d-%d", strings.ToLower(rawType), tid, start1-1, end1)
		parents = []string{tid}
	default:
		id = fmt.Sprintf("%s:%s:%d-%d", rawType, tid, start1-1, end1)
		if tid != "" {
			parents = []string{tid}
		}
	}
	f = model.Feature{
		ID: id, Version: 1, Type: ftype, SeqID: cols[0],
		Start: start1 - 1, End: end1, Strand: strand,
		Parents: parents, Source: opts.Source, URI: opts.URI, Line: lineNo,
		RawType: rawType, Attrs: attrs,
	}
	if ftype != model.FeatureGene && gid != "" && gid != id {
		return f, gid, cols[0], nil
	}
	return f, "", "", nil
}

func parseGTFAttributes(s string) (map[string]string, error) {
	attrs := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return attrs, nil
	}
	for _, item := range strings.Split(s, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		k, v, ok := strings.Cut(item, " ")
		if !ok {
			k, v, ok = strings.Cut(item, "\t")
		}
		if !ok {
			return nil, fmt.Errorf("malformed attribute %q", item)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		attrs[k] = v
	}
	return attrs, nil
}
