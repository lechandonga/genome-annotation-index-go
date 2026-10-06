// Command gai is the local command-line front-end for the genome annotation
// index. It works entirely on local files and a local index directory.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lechandonga/genome-annotation-index-go/pkg/index"
	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/parse"
	"github.com/lechandonga/genome-annotation-index-go/pkg/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gai:", err)
		var rerr *model.RejectError
		if errors.As(err, &rerr) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("subcommand required")
	}
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "import":
		return cmdImport(rest)
	case "query":
		return cmdQuery(rest)
	case "rollback":
		return cmdRollback(rest)
	case "rebuild":
		return cmdRebuild(rest)
	case "info":
		return cmdInfo(rest)
	case "children":
		return cmdChildren(rest)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gai - local genome annotation index

Usage:
  gai import   --index DIR --file FILE [--format gff3|gtf|bed|fasta] [--source NAME] [--lenient-seq]
  gai query    --index DIR --seq SEQ_ID --start N --end N [--strand +|-|.] [--type gene,transcript,exon]
  gai rollback --index DIR --id ID --version N
  gai rebuild  --index DIR [--from FILE ...]
  gai children --index DIR --id ID
  gai info     --index DIR

Coordinates are always 0-based half-open on input and output.
`)
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	dir := fs.String("index", ".gai-index", "index directory")
	file := fs.String("file", "", "source file")
	format := fs.String("format", "", "gff3|gtf|bed|fasta (auto-detect by extension)")
	source := fs.String("source", "", "logical source label")
	lenient := fs.Bool("lenient-seq", false, "allow unknown reference sequences")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("--file is required")
	}
	st, err := store.Open(store.Config{Dir: *dir, LenientSequences: *lenient})
	if err != nil {
		return err
	}
	rep, err := st.ImportFile(*file, *source, parse.Format(*format))
	if err != nil {
		for _, r := range rep.Rejections {
			fmt.Fprintf(os.Stderr, "REJECTED [%s] %s\n", r.Kind, r.Error())
		}
		return fmt.Errorf("import failed: %w", err)
	}
	fmt.Printf("imported: new_features=%d new_sequences=%d noop=%d generation=%d\n",
		rep.Accepted, rep.Sequences, rep.Noop, st.Generation())
	return nil
}

func cmdQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	dir := fs.String("index", ".gai-index", "")
	seq := fs.String("seq", "", "sequence/chromosome id")
	start := fs.Int("start", 0, "0-based inclusive start")
	end := fs.Int("end", 0, "0-based exclusive end")
	strand := fs.String("strand", "", "+, - or empty/.")
	typeList := fs.String("type", "", "comma-separated: gene,transcript,exon,CDS,other")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *seq == "" || *end <= *start {
		return errors.New("--seq and a valid half-open --start/--end are required")
	}
	st, err := store.Open(store.Config{Dir: *dir})
	if err != nil {
		return err
	}
	q := index.Query{SeqID: *seq, Start: *start, End: *end, StrandFilter: *strand}
	for _, t := range splitTypes(*typeList) {
		q.Types = append(q.Types, t)
	}
	hits := st.Query(q)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, h := range hits {
			if err := enc.Encode(h.Feature); err != nil {
				return err
			}
		}
		return nil
	}
	for _, h := range hits {
		f := h.Feature
		fmt.Printf("%s\tv%d\t%s\t%s\t[%d,%d)\t%s\tparents=%v\n",
			f.ID, f.Version, f.Type, f.SeqID, f.Start, f.End, f.Strand, f.Parents)
	}
	fmt.Printf("# %d hit(s), generation=%d\n", len(hits), st.Generation())
	return nil
}

func splitTypes(s string) []model.FeatureType {
	if s == "" {
		return nil
	}
	var out []model.FeatureType
	for _, t := range splitComma(s) {
		out = append(out, model.FeatureType(t))
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func cmdRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	dir := fs.String("index", ".gai-index", "")
	id := fs.String("id", "", "feature id")
	ver := fs.Int("version", 0, "target historical version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *ver == 0 {
		return errors.New("--id and --version are required")
	}
	st, err := store.Open(store.Config{Dir: *dir})
	if err != nil {
		return err
	}
	if err := st.Rollback(*id, *ver); err != nil {
		return err
	}
	fmt.Printf("rollback complete, generation=%d\n", st.Generation())
	return nil
}

func cmdRebuild(args []string) error {
	fs := flag.NewFlagSet("rebuild", flag.ContinueOnError)
	dir := fs.String("index", ".gai-index", "")
	from := fs.String("from", "", "comma-separated source files for a from-scratch rebuild")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from != "" {
		var files []store.SourceFile
		for _, p := range splitComma(*from) {
			files = append(files, store.SourceFile{Path: p, Format: parse.Detect(p), Source: p})
		}
		st, err := store.RebuildFromSources(store.Config{Dir: *dir}, files)
		if err != nil {
			return err
		}
		fmt.Printf("rebuilt from %d file(s), generation=%d\n", len(files), st.Generation())
		return nil
	}
	st, err := store.Open(store.Config{Dir: *dir})
	if err != nil {
		return err
	}
	n, err := st.Rebuild()
	if err != nil {
		return err
	}
	fmt.Printf("replayed %d import operation(s), generation=%d\n", n, st.Generation())
	return nil
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	dir := fs.String("index", ".gai-index", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(store.Config{Dir: *dir})
	if err != nil {
		return err
	}
	s := st.Snapshot()
	fmt.Printf("generation=%d features=%d sequences=%d ops=%d\n",
		s.Generation, len(s.Current), len(s.Sequences), len(s.Ops))
	for _, seq := range s.Sequences {
		fmt.Printf("sequence %s length=%d sha256=%s\n", seq.ID, seq.Length, seq.SHA256[:12]+"...")
	}
	return nil
}

func cmdChildren(args []string) error {
	fs := flag.NewFlagSet("children", flag.ContinueOnError)
	dir := fs.String("index", ".gai-index", "")
	id := fs.String("id", "", "parent feature id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("--id is required")
	}
	st, err := store.Open(store.Config{Dir: *dir})
	if err != nil {
		return err
	}
	for _, c := range st.Children(*id) {
		fmt.Printf("%s v%d %s [%d,%d) %s\n", c.ID, c.Version, c.Type, c.Start, c.End, c.Strand)
	}
	return nil
}
