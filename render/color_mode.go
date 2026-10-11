package render

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ColorMode selects whether renderers emit ANSI colour escapes.
type ColorMode int

const (
	// ColorAuto emits colour only when stdout is a terminal, NO_COLOR is unset,
	// and TERM is not "dumb". It is the default.
	ColorAuto ColorMode = iota
	// ColorAlways emits colour regardless of where stdout goes (for `less -R`).
	ColorAlways
	// ColorNever emits no colour escapes at all.
	ColorNever
)

// String returns the flag spelling of the mode.
func (m ColorMode) String() string {
	switch m {
	case ColorAlways:
		return "always"
	case ColorNever:
		return "never"
	default:
		return "auto"
	}
}

// ParseColorMode parses a --color value. The empty string means auto.
func ParseColorMode(value string) (ColorMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return ColorAuto, nil
	case "always", "force", "yes", "on":
		return ColorAlways, nil
	case "never", "no", "off", "none":
		return ColorNever, nil
	}
	return ColorAuto, fmt.Errorf("--color: unknown mode %q (want auto, always or never)", value)
}

// stdoutIsTerminal reports whether the process's stdout is a terminal. Tests
// override it to simulate piped output.
var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// colorEnabled is the resolved state; read it through ColorEnabled.
var colorEnabled bool

func init() {
	SetColorMode(ColorAuto)
}

// SetColorMode resolves mode and switches every colour variable in this
// package to its escape sequence (enabled) or the empty string (disabled).
// ColorAuto is resolved with autoColorEnabled against the real stdout and
// environment.
func SetColorMode(mode ColorMode) {
	switch mode {
	case ColorAlways:
		applyColors(true)
	case ColorNever:
		applyColors(false)
	default:
		applyColors(autoColorEnabled(stdoutIsTerminal(), os.LookupEnv))
	}
}

// ColorEnabled reports whether renderers currently emit colour escapes.
func ColorEnabled() bool {
	return colorEnabled
}

// autoColorEnabled implements ColorAuto: colour needs a terminal on stdout, an
// unset NO_COLOR (https://no-color.org), and a TERM other than "dumb".
func autoColorEnabled(isTerminal bool, lookupEnv func(string) (string, bool)) bool {
	if !isTerminal {
		return false
	}
	if _, set := lookupEnv("NO_COLOR"); set {
		return false
	}
	if value, _ := lookupEnv("TERM"); value == "dumb" {
		return false
	}
	return true
}

func applyColors(enabled bool) {
	colorEnabled = enabled
	pick := func(code string) string {
		if enabled {
			return code
		}
		return ""
	}
	Reset = pick(ansiReset)
	Bold = pick(ansiBold)
	Dim = pick(ansiDim)
	White = pick(ansiWhite)
	Cyan = pick(ansiCyan)
	Yellow = pick(ansiYellow)
	Magenta = pick(ansiMagenta)
	Green = pick(ansiGreen)
	Red = pick(ansiRed)
	Blue = pick(ansiBlue)
	BoldWhite = pick(ansiBoldWhite)
	BoldRed = pick(ansiBoldRed)
	BoldBlue = pick(ansiBoldBlue)
	DimWhite = pick(ansiDimWhite)
	BoldGreen = pick(ansiBoldGreen)
	buildingColors = buildingColors[:0]
	for _, code := range buildingPalette {
		buildingColors = append(buildingColors, pick(code))
	}
}
