package render

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// ANSI color codes. These are variables, not constants: every one of them is
// the escape sequence below while colour is enabled and the empty string while
// it is disabled, so renderers can concatenate them unconditionally. See
// SetColorMode for the switch; the default resolves ColorAuto against stdout
// when the package initializes.
var (
	Reset     = ansiReset
	Bold      = ansiBold
	Dim       = ansiDim
	White     = ansiWhite
	Cyan      = ansiCyan
	Yellow    = ansiYellow
	Magenta   = ansiMagenta
	Green     = ansiGreen
	Red       = ansiRed
	Blue      = ansiBlue
	BoldWhite = ansiBoldWhite
	BoldRed   = ansiBoldRed
	BoldBlue  = ansiBoldBlue
	DimWhite  = ansiDimWhite
	BoldGreen = ansiBoldGreen
)

// The palette: the escape sequences the exported variables take while colour
// is enabled.
const (
	ansiReset     = "\033[0m"
	ansiBold      = "\033[1m"
	ansiDim       = "\033[2m"
	ansiWhite     = "\033[37m"
	ansiCyan      = "\033[36m"
	ansiYellow    = "\033[33m"
	ansiMagenta   = "\033[35m"
	ansiGreen     = "\033[32m"
	ansiRed       = "\033[31m"
	ansiBlue      = "\033[34m"
	ansiBoldWhite = "\033[1;37m"
	ansiBoldRed   = "\033[1;31m"
	ansiBoldBlue  = "\033[1;34m"
	ansiDimWhite  = "\033[2;37m"
	ansiBoldGreen = "\033[1;32m"
)

// Asset extensions to exclude from "top large files"
var assetExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true, ".webp": true,
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	".mp3": true, ".wav": true, ".mp4": true, ".mov": true,
	".zip": true, ".tar": true, ".gz": true, ".7z": true, ".rar": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".lock": true, ".resolved": true, ".sum": true,
	".map": true, ".nib": true, ".xib": true, ".storyboard": true,
}

// GetFileColor returns ANSI color code based on file extension
func GetFileColor(ext string) string {
	ext = strings.ToLower(ext)
	switch {
	case ext == ".go" || ext == ".mod" || ext == ".sum" || ext == ".dart":
		return Cyan
	case ext == ".py" || ext == ".js" || ext == ".ts" || ext == ".jsx" || ext == ".tsx" ||
		ext == ".mjs" || ext == ".cjs" || ext == ".vue" || ext == ".svelte" ||
		ext == ".pl" || ext == ".pm" || ext == ".sql" || ext == ".db" || ext == ".sqlite":
		return Yellow
	case ext == ".html" || ext == ".css" || ext == ".scss" || ext == ".sass" ||
		ext == ".less" || ext == ".php" || ext == ".hs" || ext == ".tf" || ext == ".hcl":
		return Magenta
	case ext == ".md" || ext == ".txt" || ext == ".rst" || ext == ".adoc":
		return Green
	case ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" ||
		ext == ".xml" || ext == ".csv" || ext == ".ini" || ext == ".conf" ||
		ext == ".env" || ext == ".rb" || ext == ".erb" || ext == ".gemspec":
		return Red
	case ext == ".sh" || ext == ".bat" || ext == ".ps1" ||
		strings.ToLower(ext) == "makefile" || strings.ToLower(ext) == "dockerfile":
		return BoldWhite
	case ext == ".swift" || ext == ".kt" || ext == ".java" || ext == ".scala" ||
		ext == ".groovy" || ext == ".rs" || ext == ".rlib" || ext == ".cue":
		return BoldRed
	case ext == ".c" || ext == ".cpp" || ext == ".h" || ext == ".hpp" ||
		ext == ".cc" || ext == ".m" || ext == ".mm" || ext == ".cs" || ext == ".fs":
		return BoldBlue
	case ext == ".lua" || ext == ".r" || ext == ".rmd":
		return Blue
	case ext == ".gitignore" || ext == ".dockerignore" || ext == ".gitattributes":
		return DimWhite
	default:
		return White
	}
}

// GetTerminalWidth returns terminal width or default
func GetTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80
	}
	return width
}

// CenterString centers a string in the given width
func CenterString(s string, width int) string {
	if len(s) >= width {
		return s
	}
	leftPad := (width - len(s)) / 2
	rightPad := width - len(s) - leftPad
	return strings.Repeat(" ", leftPad) + s + strings.Repeat(" ", rightPad)
}

// IsAssetExtension returns true if the extension is an asset
func IsAssetExtension(ext string) bool {
	return assetExtensions[strings.ToLower(ext)]
}
