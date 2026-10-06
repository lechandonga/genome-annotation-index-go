package parse

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

// ParseBED parses BED6 (minimum) through BED12. Coordinates are already
// zero-based half-open and are kept as-is.
//
// Semantics:
//
//   - a BED6 row becomes one "transcript" feature named by column 4;
//   - BED12 rows with blockCount > 1 additionally yield one "exon" feature
//     per block, parented to the row feature;
//   - missing names receive a deterministic synthetic id
//     ("<source>#line<n>:<chrom>:<start>:<end>").
func ParseBED(r io.Reader, file string, opts Options) ([]model.Feature, Rejects, error) {
	br := bufio.NewScanner(r)
	br.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var features []model.Feature
	var rejects Rejects
	lineNo := 0
	sourceName := opts.SourceName
	if sourceName == "" {
		sourceName = file
	}

	for br.Scan() {
		lineNo++
		line := strings.TrimRight(br.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(strings.ToLower(trimmed), "track ") ||
			strings.HasPrefix(strings.ToLower(trimmed), "browser ") {
			continue
		}
		feats, rec := parseBEDLine(line, file, lineNo, sourceName, opts.Sequences)
		if rec != nil {
			rejects = append(rejects, rec)
			continue
		}
		features = append(features, feats...)
	}
	if err := br.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return features, rejects, nil
}

func parseBEDLine(line, file string, lineNo int, sourceName string, seqs SequenceSet) ([]model.Feature, *Reject) {
	const format = "bed"
	cols := strings.Split(line, "\t")
	if len(cols) < 3 {
		return nil, reject(format, file, lineNo, ReasonSyntax,
			"BED requires at least 3 columns, got %d", len(cols))
	}
	if len(cols) > 12 {
		return nil, reject(format, file, lineNo, ReasonSyntax,
			"BED supports at most 12 columns, got %d", len(cols))
	}
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}

	rawStart, rec := atoiField(format, file, lineNo, cols[1], "chromStart")
	if rec != nil {
		return nil, rec
	}
	rawEnd, rec := atoiField(format, file, lineNo, cols[2], "chromEnd")
	if rec != nil {
		return nil, rec
	}
	iv, err := coord.FromRaw(coord.ZeroBasedHalfOpen, rawStart, rawEnd)
	if err != nil {
		return nil, reject(format, file, lineNo, ReasonBadCoordinateSystem, "%v", err)
	}

	name := ""
	strand := model.Unstranded
	if len(cols) >= 4 {
		name = cols[3]
	}
	if len(cols) >= 6 {
		if s, err := model.ParseStrand(cols[5]); err != nil {
			return nil, reject(format, file, lineNo, ReasonBadStrand, "%v", err)
		} else {
			strand = s
		}
	}

	if name == "" {
		name = fmt.Sprintf("%s#line%d:%s:%d:%d", sourceName, lineNo, cols[0], iv.Start, iv.End)
	}

	tx := model.Feature{
		ID:       name,
		Type:     model.Transcript,
		SeqID:    cols[0],
		Strand:   strand,
		Interval: iv,
		Source: model.Source{
			Name:   sourceName,
			Format: format,
			File:   file,
		},
		Line: lineNo,
	}
	if rej := validateAndBuild(format, &tx, seqs); rej != nil {
		return nil, rej
	}
	out := []model.Feature{tx}

	if len(cols) == 12 {
		exons, rej := parseBEDBlocks(cols, tx, file, lineNo, sourceName, seqs)
		if rej != nil {
			return nil, rej
		}
		out = append(out, exons...)
	}
	return out, nil
}

func parseBEDBlocks(cols []string, tx model.Feature, file string, lineNo int, sourceName string, seqs SequenceSet) ([]model.Feature, *Reject) {
	const format = "bed"
	blockCount, rec := atoiField(format, file, lineNo, cols[9], "blockCount")
	if rec != nil {
		return nil, rec
	}
	if blockCount < 1 {
		return nil, reject(format, file, lineNo, ReasonBlockInvalid,
			"blockCount must be >= 1, got %d", blockCount)
	}
	starts := strings.Split(strings.TrimSuffix(cols[11], ","), ",")
	sizes := strings.Split(strings.TrimSuffix(cols[10], ","), ",")
	rawStarts := cols[11]
	rawSizes := cols[10]
	if strings.TrimSpace(rawStarts) == "" || strings.TrimSpace(rawSizes) == "" {
		return nil, reject(format, file, lineNo, ReasonBlockInvalid,
			"block sizes/starts must not be empty when blockCount is given")
	}
	if len(starts) != blockCount || len(sizes) != blockCount {
		return nil, reject(format, file, lineNo, ReasonBlockInvalid,
			"blockCount=%d but got %d sizes and %d starts", blockCount, len(sizes), len(starts))
	}

	var exons []model.Feature
	prevEnd := -1
	for i := 0; i < blockCount; i++ {
		bs, err := strconv.Atoi(starts[i])
		if err != nil {
			return nil, reject(format, file, lineNo, ReasonBadNumber,
				"blockStart[%d] not integer: %q", i, starts[i])
		}
		bz, err := strconv.Atoi(sizes[i])
		if err != nil {
			return nil, reject(format, file, lineNo, ReasonBadNumber,
				"blockSize[%d] not integer: %q", i, sizes[i])
		}
		if bs < 0 || bz <= 0 {
			return nil, reject(format, file, lineNo, ReasonBlockInvalid,
				"block %d has start=%d size=%d", i, bs, bz)
		}
		start := tx.Interval.Start + bs
		end := start + bz
		if end > tx.Interval.End {
			return nil, reject(format, file, lineNo, ReasonOutOfRange,
				"block %d [%d,%d) exceeds parent interval ending at %d", i, start, end, tx.Interval.End)
		}
		if start < prevEnd && prevEnd != -1 {
			return nil, reject(format, file, lineNo, ReasonBlockInvalid,
				"blocks must be non-overlapping and sorted; block %d starts %d before previous end %d",
				i, start, prevEnd)
		}
		prevEnd = end
		iv, err := coord.NewInterval(start, end)
		if err != nil {
			return nil, reject(format, file, lineNo, ReasonBadCoordinateSystem, "%v", err)
		}
		e := model.Feature{
			ID:        fmt.Sprintf("%s.exon%d", tx.ID, i+1),
			Type:      model.Exon,
			SeqID:     tx.SeqID,
			Strand:    tx.Strand,
			Interval:  iv,
			ParentIDs: []string{tx.ID},
			Source:    tx.Source,
			Line:      lineNo,
		}
		if rej := validateAndBuild(format, &e, seqs); rej != nil {
			return nil, rej
		}
		exons = append(exons, e)
	}
	return exons, nil
}
