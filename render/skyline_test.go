package render

import (
	"bytes"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/scanner"

	tea "github.com/charmbracelet/bubbletea"
)

func resetSkylineRNG() {
	rng = rand.New(rand.NewPCG(42, 0))
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

func TestSkylineAnimationModelInitAndPhase2Transitions(t *testing.T) {
	resetSkylineRNG()

	tests := []struct {
		name     string
		model    animationModel
		msg      tea.Msg
		assertFn func(t *testing.T, before animationModel, after animationModel, cmd tea.Cmd)
	}{
		{
			name:  "init returns tick command",
			model: animationModel{},
			msg:   nil,
			assertFn: func(t *testing.T, before animationModel, _ animationModel, cmd tea.Cmd) {
				t.Helper()
				if before.Init() == nil {
					t.Fatal("expected non-nil init command")
				}
				if cmd != nil {
					t.Fatal("expected nil command for nil update message")
				}
			},
		},
		{
			name: "phase 2 activates shooting star at frame 10",
			model: animationModel{
				phase:      2,
				frame:      9,
				sceneLeft:  2,
				sceneRight: 20,
			},
			msg: tickMsg(time.Now()),
			assertFn: func(t *testing.T, before animationModel, after animationModel, cmd tea.Cmd) {
				t.Helper()
				if cmd == nil {
					t.Fatal("expected tick command")
				}
				if after.frame != before.frame+1 {
					t.Fatalf("frame = %d, want %d", after.frame, before.frame+1)
				}
				if !after.shootingStarActive {
					t.Fatal("expected shooting star to activate")
				}
				if after.shootingStarCol != before.sceneLeft {
					t.Fatalf("shootingStarCol = %d, want %d", after.shootingStarCol, before.sceneLeft)
				}
				if after.shootingStarRow < 0 || after.shootingStarRow > 2 {
					t.Fatalf("shootingStarRow out of range: %d", after.shootingStarRow)
				}
			},
		},
		{
			name: "active shooting star advances and can deactivate",
			model: animationModel{
				phase:              2,
				frame:              25,
				sceneRight:         10,
				shootingStarActive: true,
				shootingStarCol:    9,
			},
			msg: tickMsg(time.Now()),
			assertFn: func(t *testing.T, _ animationModel, after animationModel, cmd tea.Cmd) {
				t.Helper()
				if cmd == nil {
					t.Fatal("expected tick command")
				}
				if after.shootingStarActive {
					t.Fatal("expected shooting star to deactivate after leaving scene")
				}
			},
		},
		{
			name: "phase 2 quits after frame 40",
			model: animationModel{
				phase: 2,
				frame: 39,
			},
			msg: tickMsg(time.Now()),
			assertFn: func(t *testing.T, _ animationModel, after animationModel, cmd tea.Cmd) {
				t.Helper()
				if cmd == nil {
					t.Fatal("expected quit command")
				}
				if !after.done {
					t.Fatal("expected model to be marked done")
				}
			},
		},
		{
			name: "non tick message returns nil command",
			model: animationModel{
				phase: 1,
			},
			msg: struct{}{},
			assertFn: func(t *testing.T, _ animationModel, _ animationModel, cmd tea.Cmd) {
				t.Helper()
				if cmd != nil {
					t.Fatal("expected nil command for unknown message type")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.model
			updated, cmd := tt.model.Update(tt.msg)
			after := updated.(animationModel)
			tt.assertFn(t, before, after, cmd)
		})
	}
}

func TestSkylineAnimationModelViewPhase2ShootingStar(t *testing.T) {
	resetSkylineRNG()

	m := animationModel{
		arranged: []building{{height: 4, char: '▓', color: Cyan, extLabel: ".go", gap: 1}},
		width:    24,
		phase:    2,

		leftMargin:         2,
		sceneLeft:          1,
		sceneRight:         20,
		sceneWidth:         19,
		starPositions:      [][2]int{{0, 3}},
		moonCol:            8,
		maxBuildingHeight:  4,
		visibleRows:        4,
		shootingStarRow:    0,
		shootingStarCol:    5,
		shootingStarActive: true,
	}

	view := m.View()
	checks := []string{"★", "◐", "▀"}
	for _, check := range checks {
		if !strings.Contains(view, check) {
			t.Fatalf("expected view to contain %q, got:\n%s", check, view)
		}
	}
}

func TestSkylineUsesRootBasenameWhenProjectNameMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "example-project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	project := scanner.Project{
		Root: root,
		Files: []scanner.FileInfo{
			{Path: "main.go", Ext: ".go", Size: 200},
			{Path: "utils.ts", Ext: ".ts", Size: 100},
		},
	}

	var buf bytes.Buffer
	Skyline(&buf, project, true)

	out := buf.String()
	if strings.Contains(out, "No source files to display") {
		t.Fatalf("expected skyline output, got:\n%s", out)
	}
	if !strings.Contains(out, "example-project") {
		t.Fatalf("expected output to include fallback project name, got:\n%s", out)
	}
	if !strings.Contains(out, "languages") {
		t.Fatalf("expected summary line in output, got:\n%s", out)
	}
}

func TestSkylineAnimatePathCallsRenderAnimatedForStdout(t *testing.T) {
	project := scanner.Project{
		Root: t.TempDir(),
		Name: "Demo",
		Files: []scanner.FileInfo{
			{Path: "main.go", Ext: ".go", Size: 100},
		},
	}

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = origStdout
	})

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	Skyline(w, project, true)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := <-done
	if !strings.Contains(out, "Demo") {
		t.Fatalf("expected skyline output to include project name, got:\n%s", out)
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
