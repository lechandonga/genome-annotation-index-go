package persist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/persist"
	"github.com/lechandonga/genome-annotation-index-go/pkg/registry"
	"github.com/lechandonga/genome-annotation-index-go/pkg/testutil"
)

func sample() *registry.Snapshot {
	r := registry.New(registry.Config{RequireSequences: false})
	s, rep := r.Apply(registry.Batch{
		Sequences: []model.Sequence{{ID: "c", Length: 100, SHA256: "x"}},
		Features: []model.Feature{{
			ID: "g1", Version: 1, Type: model.FeatureOther, SeqID: "c",
			Start: 0, End: 10, Strand: "+", Attrs: map[string]string{},
		}}})
	if len(rep.Rejections) != 0 {
		panic(rep.Rejections[0].Error())
	}
	return s
}

func TestSnapshotIntegrity(t *testing.T) {
	dir := t.TempDir()
	testutil.RunGroup(t, "persist.integrity", []testutil.Case{
		{
			Name: "save then load round-trips",
			Run: func() string {
				if err := persist.Save(dir, sample()); err != nil {
					return err.Error()
				}
				s, err := persist.Load(dir)
				if err != nil {
					return err.Error()
				}
				if s == nil || s.Generation != 1 || len(s.Current) != 1 {
					return "loaded snapshot wrong"
				}
				return ""
			},
		},
		{
			Name: "missing snapshot loads as empty (safe fresh start)",
			Run: func() string {
				d := filepath.Join(t.TempDir(), "fresh")
				s, err := persist.Load(d)
				if err != nil || s != nil {
					return "expected (nil,nil)"
				}
				return ""
			},
		},
		{
			Name: "truncated snapshot rejected",
			Run: func() string {
				d := t.TempDir()
				if err := persist.Save(d, sample()); err != nil {
					return err.Error()
				}
				p, _ := persist.Paths(d)
				data, _ := os.ReadFile(p)
				if err := os.WriteFile(p, data[:len(data)/2], 0o644); err != nil {
					return err.Error()
				}
				if _, err := persist.Load(d); err == nil {
					return "truncated snapshot accepted"
				}
				return ""
			},
		},
		{
			Name: "wrong checksum rejected",
			Run: func() string {
				d := t.TempDir()
				if err := persist.Save(d, sample()); err != nil {
					return err.Error()
				}
				_, sumPath := persist.Paths(d)
				if err := os.WriteFile(sumPath, []byte("deadbeef  snapshot.json\n"), 0o644); err != nil {
					return err.Error()
				}
				_, err := persist.Load(d)
				if err == nil {
					return "bad checksum accepted"
				}
				var rerr *model.RejectError
				if !asReject(err, &rerr) || rerr.Kind != model.RejectSnapshot {
					return "error is not RejectSnapshot"
				}
				return ""
			},
		},
		{
			Name: "leftover temp file never treated as snapshot",
			Run: func() string {
				d := t.TempDir()
				if err := os.WriteFile(filepath.Join(d, "snapshot.json.tmp"), []byte("{garbage"), 0o644); err != nil {
					return err.Error()
				}
				s, err := persist.Load(d)
				if err != nil || s != nil {
					return "temp file should be ignored"
				}
				return ""
			},
		},
	})
}

func asReject(err error, target **model.RejectError) bool {
	for err != nil {
		if r, ok := err.(*model.RejectError); ok {
			*target = r
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
