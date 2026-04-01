package render

import (
	"bytes"
	"math/rand/v2"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"codemap/scanner"

	tea "github.com/charmbracelet/bubbletea"
)

var skylineANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func resetSkylineRNG() {
	rng = rand.New(rand.NewPCG(42, 0))
}

func stripSkylineANSI(s string) string {
	return skylineANSIPattern.ReplaceAllString(s, "")
}

func TestSkylineFilterCodeFiles(t *testing.T) {
	tests := []struct {
		name     string
		files    []scanner.FileInfo
		expected int
	}{
		{
			name: "returns only code files when present",
			files: []scanner.FileInfo{
				{Path: "main.go", Ext: ".go"},
				{Path: "photo.png", Ext: ".png"},
				{Path: "Dockerfile"},
			},
			expected: 2,
		},
		{
			name: "returns original files when no code files found",
			files: []scanner.FileInfo{
				{Path: "image.png", Ext: ".png"},
				{Path: "font.woff", Ext: ".woff"},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterCodeFiles(tt.files)
			if len(got) != tt.expected {
				t.Fatalf("filterCodeFiles() len = %d, want %d", len(got), tt.expected)
			}
		})
	}
}

func TestSkylineAggregateByExtension(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "a/main.go", Ext: ".go", Size: 100},
		{Path: "a/util.go", Ext: ".go", Size: 50},
		{Path: "b/app.ts", Ext: ".ts", Size: 120},
		{Path: "Makefile", Ext: "", Size: 80},
	}

	agg := aggregateByExtension(files)
	if len(agg) != 3 {
		t.Fatalf("aggregateByExtension() len = %d, want 3", len(agg))
	}

	if agg[0].ext != ".go" || agg[0].size != 150 || agg[0].count != 2 {
		t.Fatalf("unexpected first aggregate: %+v", agg[0])
	}

	seenMakefile := false
	for _, a := range agg {
		if a.ext == "Makefile" {
			seenMakefile = true
			break
		}
	}
	if !seenMakefile {
		t.Fatal("expected aggregate entry for Makefile")
	}
}

func TestSkylineGetBuildingChar(t *testing.T) {
	tests := []struct {
		name     string
		ext      string
		expected rune
	}{
		{name: "go", ext: ".go", expected: '▓'},
		{name: "javascript", ext: ".js", expected: '░'},
		{name: "ruby", ext: ".rb", expected: '▒'},
		{name: "makefile", ext: "makefile", expected: '█'},
		{name: "default", ext: ".unknown", expected: '▓'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBuildingChar(tt.ext)
			if got != tt.expected {
				t.Fatalf("getBuildingChar(%q) = %q, want %q", tt.ext, got, tt.expected)
			}
		})
	}
}

func TestSkylineCreateBuildings(t *testing.T) {
	resetSkylineRNG()

	tests := []struct {
		name        string
		sorted      []extAgg
		width       int
		wantNil     bool
		maxTotalW   int
		wantNonZero bool
	}{
		{name: "empty input", sorted: nil, width: 80, wantNil: true},
		{
			name: "fits within width",
			sorted: []extAgg{
				{ext: ".go", size: 1000, count: 3},
				{ext: ".ts", size: 700, count: 2},
				{ext: ".py", size: 300, count: 1},
			},
			width:       80,
			maxTotalW:   72,
			wantNonZero: true,
		},
		{
			name: "trims buildings for narrow width",
			sorted: []extAgg{
				{ext: ".go", size: 1000, count: 3},
				{ext: ".ts", size: 900, count: 2},
				{ext: ".py", size: 800, count: 1},
				{ext: ".rb", size: 700, count: 1},
			},
			width:       22,
			maxTotalW:   14,
			wantNonZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSkylineRNG()
			got := createBuildings(tt.sorted, tt.width)
			if tt.wantNil {
				if got != nil {
					t.Fatal("expected nil buildings")
				}
				return
			}
			if tt.wantNonZero && len(got) == 0 {
				t.Fatal("expected non-empty buildings")
			}

			totalWidth := 0
			for _, b := range got {
				totalWidth += buildingWidth + b.gap
				if b.height < minHeight || b.height > maxHeight {
					t.Fatalf("building height out of range: %d", b.height)
				}
			}
			if totalWidth > tt.maxTotalW {
				t.Fatalf("total building width = %d, want <= %d", totalWidth, tt.maxTotalW)
			}
		})
	}
}

func TestSkylineNoSourceFilesMessage(t *testing.T) {
	project := scanner.Project{Root: t.TempDir(), Name: "Demo", Files: nil}
	var buf bytes.Buffer

	Skyline(&buf, project, false)

	out := buf.String()
	if !strings.Contains(out, "No source files to display") {
		t.Fatalf("expected no files message, got:\n%s", out)
	}
}

func TestSkylineRenderStaticIncludesTitleAndStats(t *testing.T) {
	resetSkylineRNG()

	arranged := []building{{
		height:   6,
		char:     '▓',
		color:    Cyan,
		ext:      ".go",
		extLabel: ".go",
		count:    2,
		size:     300,
		gap:      1,
	}}

	codeFiles := []scanner.FileInfo{{Path: "main.go", Size: 300, Ext: ".go"}}
	sorted := []extAgg{{ext: ".go", size: 300, count: 1}}

	var buf bytes.Buffer
	renderStatic(&buf, arranged, 40, 10, 8, 24, 16, codeFiles, "Demo", sorted)

	out := buf.String()
	checks := []string{"─── Demo ───", "1 languages", "1 files", "300.0B"}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestSkylineUsesRootBaseNameWhenNameMissing(t *testing.T) {
	resetSkylineRNG()

	root := t.TempDir()
	project := scanner.Project{
		Root: root,
		Files: []scanner.FileInfo{
			{Path: "src/main.go", Ext: ".go", Size: 256},
		},
	}

	var buf bytes.Buffer
	Skyline(&buf, project, true)

	out := stripSkylineANSI(buf.String())
	if !strings.Contains(out, "─── "+filepath.Base(root)+" ───") {
		t.Fatalf("expected skyline title to use root basename, got:\n%s", out)
	}
}

func TestSkylineAnimationModelUpdateAndView(t *testing.T) {
	resetSkylineRNG()

	m := animationModel{
		arranged:          []building{{height: 5, char: '▓', color: Cyan, extLabel: ".go", gap: 1}},
		width:             30,
		leftMargin:        3,
		sceneLeft:         1,
		sceneRight:        20,
		sceneWidth:        19,
		starPositions:     [][2]int{{0, 2}, {1, 6}},
		moonCol:           10,
		maxBuildingHeight: 5,
		phase:             1,
		visibleRows:       1,
	}

	updated, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected tick command after tick update")
	}
	m1 := updated.(animationModel)
	if m1.visibleRows <= m.visibleRows {
		t.Fatalf("expected visibleRows to increase, got %d -> %d", m.visibleRows, m1.visibleRows)
	}

	out := m1.View()
	if !strings.Contains(out, "▀") {
		t.Fatalf("expected skyline ground in view, got:\n%s", out)
	}

	updated, cmd = m1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command on key press")
	}
	m2 := updated.(animationModel)
	if !m2.done {
		t.Fatal("expected model to be marked done after key press")
	}
}

func TestAnimationModelInitAndPhaseTransitions(t *testing.T) {
	resetSkylineRNG()

	m := animationModel{
		arranged:          []building{{height: 3, char: '▓', color: Cyan, extLabel: ".go", gap: 1}},
		width:             20,
		leftMargin:        2,
		sceneLeft:         1,
		sceneRight:        12,
		sceneWidth:        11,
		maxBuildingHeight: 3,
		phase:             1,
		visibleRows:       5,
	}

	if cmd := m.Init(); cmd == nil {
		t.Fatal("expected Init to return a tick command")
	}

	updated, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected tick command during rising phase")
	}

	m1 := updated.(animationModel)
	if m1.phase != 2 {
		t.Fatalf("expected phase transition to 2, got %d", m1.phase)
	}
	if m1.frame != 0 {
		t.Fatalf("expected frame reset after phase transition, got %d", m1.frame)
	}

	m1.frame = 39
	updated, cmd = m1.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected quit command when animation completes")
	}

	m2 := updated.(animationModel)
	if !m2.done {
		t.Fatal("expected animation model to be marked done")
	}
}

func TestAnimationModelUpdateShootingStarLifecycle(t *testing.T) {
	resetSkylineRNG()

	m := animationModel{
		arranged:           []building{{height: 4, char: '▓', color: Cyan, extLabel: ".go", gap: 1}},
		width:              20,
		leftMargin:         2,
		sceneLeft:          3,
		sceneRight:         10,
		sceneWidth:         7,
		maxBuildingHeight:  4,
		phase:              2,
		frame:              9,
		shootingStarActive: false,
	}

	updated, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected tick command in twinkling phase")
	}

	m1 := updated.(animationModel)
	if !m1.shootingStarActive {
		t.Fatal("expected shooting star to activate on frame 10")
	}
	if m1.shootingStarCol != m.sceneLeft {
		t.Fatalf("expected shooting star to start at scene left %d, got %d", m.sceneLeft, m1.shootingStarCol)
	}

	m1.shootingStarCol = m1.sceneRight + 1
	updated, cmd = m1.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected tick command when advancing active shooting star")
	}

	m2 := updated.(animationModel)
	if m2.shootingStarActive {
		t.Fatal("expected shooting star to deactivate after leaving the scene")
	}
}

func TestAnimationModelViewRendersLabelsAndShootingStar(t *testing.T) {
	resetSkylineRNG()

	m := animationModel{
		arranged: []building{
			{height: 4, char: '▓', color: Cyan, extLabel: ".go", gap: 1},
			{height: 4, char: '▒', color: Yellow, extLabel: "A-1", gap: 1},
		},
		width:              24,
		leftMargin:         2,
		sceneLeft:          1,
		sceneRight:         20,
		sceneWidth:         19,
		starPositions:      [][2]int{{0, 2}},
		moonCol:            12,
		maxBuildingHeight:  4,
		phase:              2,
		visibleRows:        6,
		shootingStarActive: true,
		shootingStarRow:    0,
		shootingStarCol:    4,
	}

	out := stripSkylineANSI(m.View())
	for _, want := range []string{".go", "A-1", "★", "◐", "▀"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, out)
		}
	}
}

func TestSkylineMinMax(t *testing.T) {
	tests := []struct {
		name    string
		a       int
		b       int
		wantMax int
		wantMin int
	}{
		{name: "a greater", a: 8, b: 3, wantMax: 8, wantMin: 3},
		{name: "b greater", a: 2, b: 9, wantMax: 9, wantMin: 2},
		{name: "equal", a: 5, b: 5, wantMax: 5, wantMin: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := max(tt.a, tt.b); got != tt.wantMax {
				t.Fatalf("max(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.wantMax)
			}
			if got := min(tt.a, tt.b); got != tt.wantMin {
				t.Fatalf("min(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.wantMin)
			}
		})
	}
}
