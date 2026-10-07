package render

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"codemap/scanner"
)

// withStdoutTerminal stubs the stdout TTY check for one test.
func withStdoutTerminal(t *testing.T, isTTY bool) {
	t.Helper()
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTTY }
	t.Cleanup(func() {
		stdoutIsTerminal = prev
		SetColorMode(ColorAuto)
	})
}

func colorModeFixture() scanner.Project {
	return scanner.Project{
		Name: "demo",
		Root: "/tmp/demo",
		Files: []scanner.FileInfo{
			{Path: "src/app/main.go", Ext: ".go", Size: 120},
			{Path: "src/app/util.go", Ext: ".go", Size: 80},
			{Path: "web/index.html", Ext: ".html", Size: 40},
			{Path: "README.md", Ext: ".md", Size: 30},
		},
	}
}

func TestTreePipedOutputHasNoANSIEscapes(t *testing.T) {
	withStdoutTerminal(t, false)
	t.Setenv("NO_COLOR", "") // registers restore; then unset so NO_COLOR is not what disables colour
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	SetColorMode(ColorAuto)
	if ColorEnabled() {
		t.Fatal("ColorEnabled() = true with stdout piped, want false")
	}

	var buf bytes.Buffer
	Tree(&buf, colorModeFixture())
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("piped tree output contains ANSI escapes:\n%q", out)
	}
	for _, want := range []string{"demo", "src/app/", "main", "util", "README.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("piped tree output lost %q:\n%s", want, out)
		}
	}
}

func TestTreeColorAlwaysRestoresANSIEscapes(t *testing.T) {
	withStdoutTerminal(t, false)
	t.Setenv("NO_COLOR", "1")
	SetColorMode(ColorAlways)
	if !ColorEnabled() {
		t.Fatal("ColorEnabled() = false under ColorAlways, want true")
	}

	var buf bytes.Buffer
	Tree(&buf, colorModeFixture())
	out := buf.String()
	if !strings.Contains(out, ansiBoldBlue+"  src/app/"+ansiReset) {
		t.Fatalf("--color=always output lacks the directory colour:\n%q", out)
	}
	if !strings.Contains(out, ansiBold+"demo"+ansiReset) {
		t.Fatalf("--color=always output lacks the bold project name:\n%q", out)
	}
	if stripTreeANSI(out) == out {
		t.Fatal("--color=always output has no ANSI escapes at all")
	}
}

func TestTreeColorNeverOnTerminal(t *testing.T) {
	withStdoutTerminal(t, true)
	SetColorMode(ColorNever)
	var buf bytes.Buffer
	Tree(&buf, colorModeFixture())
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("--color=never output contains ANSI escapes:\n%q", buf.String())
	}
}

func TestSkylineStaticHonoursColorMode(t *testing.T) {
	withStdoutTerminal(t, false)
	project := colorModeFixture()

	SetColorMode(ColorNever)
	var off bytes.Buffer
	Skyline(&off, project, false)
	if strings.Contains(off.String(), "\x1b[") {
		t.Fatalf("skyline with colour off contains ANSI escapes:\n%q", off.String())
	}
	for _, code := range buildingColors {
		if code != "" {
			t.Fatalf("buildingColors still carries %q with colour off", code)
		}
	}

	SetColorMode(ColorAlways)
	var on bytes.Buffer
	Skyline(&on, project, false)
	if !strings.Contains(on.String(), "\x1b[") {
		t.Fatalf("skyline with colour on has no ANSI escapes:\n%q", on.String())
	}
	if len(buildingColors) != len(buildingPalette) {
		t.Fatalf("buildingColors has %d entries, want %d", len(buildingColors), len(buildingPalette))
	}
	for i, code := range buildingColors {
		if code != buildingPalette[i] {
			t.Fatalf("buildingColors[%d] = %q, want %q", i, code, buildingPalette[i])
		}
	}
}

func TestAutoColorEnabled(t *testing.T) {
	env := func(vars map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			value, ok := vars[key]
			return value, ok
		}
	}
	tests := []struct {
		name  string
		isTTY bool
		vars  map[string]string
		want  bool
	}{
		{"terminal", true, map[string]string{"TERM": "xterm-256color"}, true},
		{"piped", false, map[string]string{"TERM": "xterm-256color"}, false},
		{"terminal NO_COLOR=1", true, map[string]string{"NO_COLOR": "1"}, false},
		{"terminal NO_COLOR set but empty", true, map[string]string{"NO_COLOR": ""}, false},
		{"terminal TERM=dumb", true, map[string]string{"TERM": "dumb"}, false},
		{"terminal no TERM", true, map[string]string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoColorEnabled(tt.isTTY, env(tt.vars)); got != tt.want {
				t.Fatalf("autoColorEnabled(%v, %v) = %v, want %v", tt.isTTY, tt.vars, got, tt.want)
			}
		})
	}
}

func TestParseColorMode(t *testing.T) {
	good := map[string]ColorMode{
		"": ColorAuto, "auto": ColorAuto, "AUTO": ColorAuto,
		"always": ColorAlways, "force": ColorAlways, "on": ColorAlways, "yes": ColorAlways,
		"never": ColorNever, "off": ColorNever, "no": ColorNever, "none": ColorNever,
	}
	for in, want := range good {
		got, err := ParseColorMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseColorMode(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"sometimes", "1", "true"} {
		if _, err := ParseColorMode(bad); err == nil {
			t.Fatalf("ParseColorMode(%q) accepted an unknown mode", bad)
		}
	}
	for _, m := range []ColorMode{ColorAuto, ColorAlways, ColorNever} {
		back, err := ParseColorMode(m.String())
		if err != nil || back != m {
			t.Fatalf("ParseColorMode(%q) = %v, %v; want round trip", m.String(), back, err)
		}
	}
}
