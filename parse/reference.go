package parse

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// SequenceSet maps reference sequence names to their lengths.
type SequenceSet map[string]int

// ParseFASTA reads a FASTA stream and records sequence lengths. Headers use
// the first whitespace-delimited token after '>' as the sequence id.
//
// Duplicate sequence ids, empty ids and duplicate headers with differing
// lengths are rejected rather than silently concatenated. Bases outside the
// IUPAC/FASTA alphabet produce syntax rejections.
func ParseFASTA(r io.Reader, file string) (SequenceSet, Rejects, error) {
	br := bufio.NewScanner(r)
	br.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	seqs := SequenceSet{}
	var rejects Rejects

	var curID string
	curLen := 0
	lineNo := 0
	flush := func() error {
		if curID == "" {
			return nil
		}
		if existing, ok := seqs[curID]; ok {
			return reject("fasta", file, lineNo, ReasonDuplicateRecord,
				"duplicate sequence id %q (previous length %d)", curID, existing)
		}
		seqs[curID] = curLen
		return nil
	}
	seenHeader := false
	for br.Scan() {
		lineNo++
		line := strings.TrimRight(br.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, ">") {
			seenHeader = true
			if err := flush(); err != nil {
				return nil, nil, err
			}
			hdr := strings.TrimSpace(strings.TrimPrefix(line, ">"))
			if hdr == "" {
				return nil, nil, reject("fasta", file, lineNo, ReasonBadHeader, "empty sequence header")
			}
			curID = strings.Fields(hdr)[0]
			curLen = 0
			continue
		}
		if !seenHeader {
			return nil, nil, reject("fasta", file, lineNo, ReasonSyntax, "sequence data before first header")
		}
		for _, c := range line {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '*', c == '-':
			default:
				return nil, nil, reject("fasta", file, lineNo, ReasonSyntax,
					"invalid base character %q", string(c))
			}
		}
		curLen += len(strings.ReplaceAll(line, "-", ""))
	}
	if err := br.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return seqs, rejects, nil
}
