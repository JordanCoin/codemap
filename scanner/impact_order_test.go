package scanner

import (
	"fmt"
	"reflect"
	"testing"
)

// usageCounts is a map, and the sort was by count alone, so changed files with
// the same importer count came out in map order: the diff impact footer and
// blast-radius JSON reordered between identical runs, and an output budget
// then kept a different subset each time.
func TestAnalyzeImpactOrderIsDeterministicAcrossTies(t *testing.T) {
	var changed []FileInfo
	var analyses []FileAnalysis
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("src/c%02d.ts", i)
		changed = append(changed, FileInfo{Path: name})
		// every changed file is imported by exactly two others: all tied
		for j := 0; j < 2; j++ {
			analyses = append(analyses, FileAnalysis{
				Path:    fmt.Sprintf("src/user%02d_%d.ts", i, j),
				Imports: []string{"./" + fmt.Sprintf("c%02d", i)},
			})
		}
	}
	// one file with more importers must lead
	analyses = append(analyses, FileAnalysis{Path: "src/extra.ts", Imports: []string{"./c07"}})

	first := AnalyzeImpactFromAnalyses(changed, analyses)
	if len(first) != 12 || first[0].File != "c07.ts" || first[0].UsedBy != 3 {
		t.Fatalf("impacts = %+v, want c07.ts (3) first", first)
	}
	for i := 2; i < len(first); i++ {
		if first[i-1].UsedBy == first[i].UsedBy && first[i-1].File > first[i].File {
			t.Fatalf("tie not broken by path: %s before %s", first[i-1].File, first[i].File)
		}
	}
	for run := 1; run < 8; run++ {
		if got := AnalyzeImpactFromAnalyses(changed, analyses); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d = %+v, want %+v", run, got, first)
		}
	}
}
