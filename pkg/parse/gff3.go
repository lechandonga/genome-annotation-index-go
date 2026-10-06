package parse

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// ParseGFF3 parses GFF3 (tab separated, 9 columns, 1-based closed coords).
func ParseGFF3(r io.Reader, opts Options) Result {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "##") || strings.HasPrefix(line, "#") {
			continue
		}
		f, rej := parseGFF3Line(line, opts, lineNo)
		if rej != nil {
			res.Rejections = append(res.Rejections, *rej)
			continue
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

var gff3TypeMap = map[string]model.FeatureType{
	"gene":       model.FeatureGene,
	"mRNA":       model.FeatureTranscript,
	"transcript": model.FeatureTranscript,
	"tRNA":       model.FeatureTranscript,
	"rRNA":       model.FeatureTranscript,
	"lnc_RNA":    model.FeatureTranscript,
	"ncRNA":      model.FeatureTranscript,
	"exon":       model.FeatureExon,
	"CDS":        model.FeatureCDS,
}

func parseGFF3Line(line string, opts Options, lineNo int) (model.Feature, *model.Rejection) {
	cols := strings.Split(line, "\t")
	if len(cols) != 9 {
		return model.Feature{}, ptrRej(model.RejectMalformed, opts.URI, lineNo,
			"expected 9 tab-separated columns, got %d", len(cols))
	}
	for i, c := range cols[:8] {
		if strings.TrimSpace(c) == "" {
			return model.Feature{}, ptrRej(model.RejectMissingField, opts.URI, lineNo,
				"column %d is empty", i+1)
		}
	}
	rawType := cols[2]
	ftype, ok := gff3TypeMap[rawType]
	if !ok {
		ftype = model.FeatureOther
	}
	start1, err := strconv.Atoi(cols[3])
	if err != nil {
		return model.Feature{}, ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "start not an integer: %q", cols[3])
	}
	end1, err := strconv.Atoi(cols[4])
	if err != nil {
		return model.Feature{}, ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "end not an integer: %q", cols[4])
	}
	if start1 < 1 || end1 < 1 {
		return model.Feature{}, ptrRej(model.RejectBadCoordinate, opts.URI, lineNo,
			"1-based coordinates must be >= 1: %d-%d", start1, end1)
	}
	if end1 < start1 {
		return model.Feature{}, ptrRej(model.RejectBadCoordinate, opts.URI, lineNo,
			"end %d precedes start %d", end1, start1)
	}
	strand := cols[6]
	if strand != model.StrandPlus && strand != model.StrandMinus && strand != model.StrandUnknown {
		return model.Feature{}, ptrRej(model.RejectBadStrand, opts.URI, lineNo, "strand %q not in + - .", strand)
	}
	attrs, err := parseGFF3Attributes(cols[8])
	if err != nil {
		return model.Feature{}, ptrRej(model.RejectBadAttribute, opts.URI, lineNo, "%v", err)
	}
	id := attrs["ID"]
	parents := splitCSV(attrs["Parent"])
	if id == "" {
		// GFF3 permits exons/CDS without ID when they hang off one parent.
		// We only accept that for leaf features and derive a deterministic ID.
		if ftype == model.FeatureExon || ftype == model.FeatureCDS {
			if len(parents) != 1 {
				return model.Feature{}, ptrRej(model.RejectMissingID, opts.URI, lineNo,
					"%s feature has no ID and %d parents (need exactly 1 to derive one)", rawType, len(parents))
			}
			id = strings.ToLower(rawType) + ":" + parents[0] + ":" + strconv.Itoa(start1-1) + "-" + strconv.Itoa(end1)
		} else {
			return model.Feature{}, ptrRej(model.RejectMissingID, opts.URI, lineNo, "attribute ID is required")
		}
	}
	return model.Feature{
		ID: id, Version: 1, Type: ftype, SeqID: cols[0],
		Start: start1 - 1, End: end1, Strand: strand,
		Parents: parents, Source: opts.Source, URI: opts.URI, Line: lineNo,
		RawType: rawType, Attrs: attrs,
	}, nil
}

func parseGFF3Attributes(s string) (map[string]string, error) {
	attrs := map[string]string{}
	if strings.TrimSpace(s) == "" || s == "." {
		return attrs, nil
	}
	for _, item := range strings.Split(s, ";") {
		if item == "" {
			return nil, strconv.ErrSyntax
		}
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("malformed GFF3 attribute %q", item)
		}
		attrs[k] = decodeGFF3(v)
	}
	return attrs, nil
}

func decodeGFF3(s string) string {
	s = strings.ReplaceAll(s, "%3B", ";")
	s = strings.ReplaceAll(s, "%3D", "=")
	s = strings.ReplaceAll(s, "%26", "&")
	s = strings.ReplaceAll(s, "%2C", ",")
	s = strings.ReplaceAll(s, "%09", "\t")
	s = strings.ReplaceAll(s, "%0A", "\n")
	s = strings.ReplaceAll(s, "%0D", "\r")
	s = strings.ReplaceAll(s, "%25", "%")
	return s
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ptrRej(kind model.RejectKind, uri string, line int, format string, args ...any) *model.Rejection {
	r := model.Rejection{Kind: kind, URI: uri, Line: line}
	r.Detail = sprintf(format, args...)
	return &r
}

func sprintf(format string, args ...any) string {
	return formatSprintf(format, args...)
}
