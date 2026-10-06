// Command gaidx is a small local CLI for the genome annotation index.
//
// Usage:
//
//	gaidx import   --dir <index> [--fasta ref.fa] [--overwrite] <file>...
//	gaidx query    --dir <index> --seq chr1 --start 10 --end 200
//	             [--strand +|-|+.|-.] [--types gene,transcript,exon] [--lineage]
//	gaidx rollback --dir <index> --to <revision>
//	gaidx rebuild  --dir <index>
//	gaidx status   --dir <index>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/importer"
	"github.com/lechandonga/genome-annotation-index-go/model"
	"github.com/lechandonga/genome-annotation-index-go/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", ".gaidx", "index directory")

	var err error
	switch cmd {
	case "import":
		fasta := fs.String("fasta", "", "reference FASTA for coordinate bounds")
		overwrite := fs.Bool("overwrite", false, "overwrite conflicting unversioned ids")
		_ = fs.Parse(os.Args[2:])
		err = runImport(*dir, *fasta, *overwrite, fs.Args())
	case "query":
		seq := fs.String("seq", "", "sequence id")
		start := fs.Int("start", 0, "0-based inclusive start")
		end := fs.Int("end", 0, "0-based exclusive end")
		strand := fs.String("strand", "", "+ | - | +. | -. | empty(any)")
		types := fs.String("types", "", "comma-separated: gene,transcript,exon,CDS,other")
		lineage := fs.Bool("lineage", false, "include overlapping ancestors")
		_ = fs.Parse(os.Args[2:])
		err = runQuery(*dir, *seq, *start, *end, *strand, *types, *lineage)
	case "rollback":
		to := fs.Int("to", 0, "target revision")
		_ = fs.Parse(os.Args[2:])
		err = runRollback(*dir, graph.Revision(*to))
	case "rebuild":
		_ = fs.Parse(os.Args[2:])
		err = runRebuild(*dir)
	case "status":
		_ = fs.Parse(os.Args[2:])
		err = runStatus(*dir)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gaidx {import|query|rollback|rebuild|status} [flags]")
}

func openIndex(dir string) (*store.Store, error) {
	return store.Open(dir, store.OpenOptions{CopySources: true})
}

func runImport(dir, fasta string, overwrite bool, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("import requires at least one file")
	}
	st, err := openIndex(dir)
	if err != nil {
		return err
	}
	defer st.Close()

	files := make([]importer.File, len(paths))
	for i, p := range paths {
		files[i] = importer.File{Path: p, SourceName: p}
	}
	conflict := graph.ConflictReject
	if overwrite {
		conflict = graph.ConflictOverwrite
	}
	rep, err := importer.Import(st, files, fasta, conflict)
	if rep != nil {
		fmt.Printf("parsed=%d rejected=%d\n", rep.Parsed, len(rep.Rejected))
		for reason, n := range rep.Rejected.Reasons() {
			fmt.Printf("  reject %s: %d\n", reason, n)
		}
		for _, r := range rep.Rejected {
			fmt.Printf("    - %s\n", r.Error())
		}
		if rep.Committed != nil {
			fmt.Printf("committed revision=%d features=%d\n", rep.Committed.Revision, rep.Committed.Count)
		}
	}
	return err
}

func runQuery(dir, seq string, start, end int, strand, typesFlag string, lineage bool) error {
	if seq == "" || end <= start {
		return fmt.Errorf("query requires --seq and --end > --start")
	}
	st, err := openIndex(dir)
	if err != nil {
		return err
	}
	defer st.Close()

	sf := store.StrandAny
	switch strand {
	case "+":
		sf = store.StrandForward
	case "-":
		sf = store.StrandReverse
	case "+.":
		sf = store.StrandForward
	case "-.":
		sf = store.StrandReverse
	case "+only":
		sf = store.StrandForwardOnly
	case "-only":
		sf = store.StrandReverseOnly
	case "":
	default:
		return fmt.Errorf("unknown strand filter %q", strand)
	}
	var types []model.FeatureType
	if typesFlag != "" {
		for _, t := range strings.Split(typesFlag, ",") {
			types = append(types, model.FeatureType(strings.TrimSpace(t)))
		}
	}
	refs := st.Query(store.Query{
		SeqID:          seq,
		Region:         coord.Interval{Start: start, End: end},
		Strand:         sf,
		Types:          types,
		IncludeLineage: lineage,
	})
	enc := json.NewEncoder(os.Stdout)
	for _, r := range refs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "revision=%d hits=%d\n", st.Revision(), len(refs))
	return nil
}

func runRollback(dir string, to graph.Revision) error {
	st, err := openIndex(dir)
	if err != nil {
		return err
	}
	defer st.Close()
	snap, err := st.Rollback(to)
	if err != nil {
		return err
	}
	fmt.Printf("rolled back to revision=%d features=%d\n", snap.Revision, len(snap.Features))
	return nil
}

func runRebuild(dir string) error {
	st, err := openIndex(dir)
	if err != nil {
		return err
	}
	defer st.Close()
	rev, err := st.Rebuild()
	if err != nil {
		return err
	}
	fmt.Printf("rebuilt revision=%d\n", rev)
	return nil
}

func runStatus(dir string) error {
	st, err := openIndex(dir)
	if err != nil {
		return err
	}
	defer st.Close()
	snap := st.Snapshot()
	fmt.Printf("revision=%d ids=%d\n", snap.Revision, len(snap.Features))
	return nil
}
