package cmd

import (
	"strings"
	"testing"
)

func TestPickTreeLeanShedsExtrasBeforeDepth(t *testing.T) {
	sizes := map[string]int{"": 120, "1": 90, "2": 60}
	render := func(lean string) string { return strings.Repeat("x", sizes[lean]) }
	for budget, want := range map[int]string{200: "", 100: "1", 70: "2", 10: "2"} {
		if got := pickTreeLean(render, budget); got != want {
			t.Errorf("budget %d: lean %q, want %q", budget, got, want)
		}
	}
}
