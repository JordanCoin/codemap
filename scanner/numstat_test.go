package scanner

import "testing"

func TestIsNumstatCount(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"4", true}, {"0", true}, {"-", true}, {"123", true},
		{"", false}, {"4a", false}, {"'dup' is ambiguous.", false}, {" ", false},
	} {
		if got := isNumstatCount(tc.in); got != tc.want {
			t.Errorf("isNumstatCount(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
