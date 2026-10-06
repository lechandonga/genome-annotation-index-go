// Package testutil provides small helpers that make test output describe
// which business capability each case covers and whether it passed.
package testutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Case is one named business test case.
type Case struct {
	Name string
	// Run returns "" on success or a failure description.
	Run func() string
}

// RunGroup executes all cases under a business heading. Output is written to
// stdout as well as the testing log so `go test -v` clearly shows coverage:
//
//	BUSINESS [parse.gff3] (3 cases)
//	  [PASS] 1-based -> 0-based half-open conversion
//	  ...
func RunGroup(t *testing.T, business string, cases []Case) {
	t.Helper()
	fmt.Fprintf(os.Stdout, "BUSINESS [%s] (%d cases)\n", business, len(cases))
	t.Logf("BUSINESS [%s] (%d cases)", business, len(cases))
	for _, c := range cases {
		t.Run(business+"/"+strings.ReplaceAll(c.Name, " ", "_"), func(t *testing.T) {
			msg := c.Run()
			if msg != "" {
				fmt.Fprintf(os.Stdout, "  [FAIL] %s -- %s\n", c.Name, msg)
				t.Fatalf("%s: %s", c.Name, msg)
			}
			fmt.Fprintf(os.Stdout, "  [PASS] %s\n", c.Name)
			t.Logf("[PASS] %s", c.Name)
		})
	}
	fmt.Fprintf(os.Stdout, "BUSINESS [%s] done\n", business)
}

// Fail builds a failure message.
func Fail(format string, args ...any) string { return fmt.Sprintf(format, args...) }
