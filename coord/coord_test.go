package coord

import "testing"

func TestFromRaw(t *testing.T) {
	cases := []struct {
		name  string
		sys   System
		s, e  int
		start int
		end   int
		errOK bool
	}{
		{"gff first base", OneBasedClosed, 1, 1, 0, 1, false},
		{"gff range", OneBasedClosed, 101, 200, 100, 200, false},
		{"gff zero rejected", OneBasedClosed, 0, 10, 0, 0, true},
		{"gff inverted", OneBasedClosed, 200, 101, 0, 0, true},
		{"bed kept", ZeroBasedHalfOpen, 0, 10, 0, 10, false},
		{"bed inverted", ZeroBasedHalfOpen, 10, 9, 0, 0, true},
		{"bed negative", ZeroBasedHalfOpen, -1, 9, 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromRaw(tc.sys, tc.s, tc.e)
			if tc.errOK {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Start != tc.start || got.End != tc.end {
				t.Fatalf("got [%d,%d), want [%d,%d)", got.Start, got.End, tc.start, tc.end)
			}
		})
	}
}

func TestOverlapAndContains(t *testing.T) {
	i := Interval{Start: 10, End: 20}
	if !i.Contains(10) || i.Contains(20) || i.Contains(9) {
		t.Fatal("half-open Contains semantics wrong")
	}
	touch := Interval{Start: 20, End: 30}
	if i.Overlaps(touch) {
		t.Fatal("intervals touching at boundary must not overlap")
	}
	if !i.Overlaps(Interval{Start: 19, End: 21}) {
		t.Fatal("1-base overlap not detected")
	}
	if err := i.CheckAgainstSequence(19); err == nil {
		t.Fatal("end beyond seq length must fail")
	}
	if err := i.CheckAgainstSequence(20); err != nil {
		t.Fatalf("end == length must be fine: %v", err)
	}
}
