package render

import (
	"strings"
	"testing"
)

func TestGetFileColor(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
	}{
		// Go files
		{".go", Cyan},
		{".mod", Cyan},
		{".dart", Cyan},
		// Python/JS
		{".py", Yellow},
		{".js", Yellow},
		{".ts", Yellow},
		{".jsx", Yellow},
		{".tsx", Yellow},
		// HTML/CSS
		{".html", Magenta},
		{".css", Magenta},
		{".scss", Magenta},
		{".php", Magenta},
		// Documentation
		{".md", Green},
		{".txt", Green},
		{".rst", Green},
		// Config files
		{".json", Red},
		{".yaml", Red},
		{".yml", Red},
		{".toml", Red},
		{".xml", Red},
		{".rb", Red},
		// Shell scripts
		{".sh", BoldWhite},
		{".bat", BoldWhite},
		// Swift/Kotlin/Java/Rust
		{".swift", BoldRed},
		{".kt", BoldRed},
		{".java", BoldRed},
		{".rs", BoldRed},
		// C/C++
		{".c", BoldBlue},
		{".cpp", BoldBlue},
		{".h", BoldBlue},
		{".hpp", BoldBlue},
		{".cs", BoldBlue},
		// Other
		{".lua", Blue},
		{".r", Blue},
		// Git files
		{".gitignore", DimWhite},
		{".dockerignore", DimWhite},
		// Unknown
		{".unknown", White},
		{"", White},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := GetFileColor(tt.ext)
			if got != tt.expected {
				t.Errorf("GetFileColor(%q) = %q, want %q", tt.ext, got, tt.expected)
			}
		})
	}
}

func TestGetFileColorCaseInsensitive(t *testing.T) {
	// Test that color detection is case-insensitive
	tests := []string{".GO", ".Go", ".go", ".PY", ".Py", ".py"}

	for _, ext := range tests {
		color := GetFileColor(ext)
		if color == White && (strings.ToLower(ext) == ".go" || strings.ToLower(ext) == ".py") {
			t.Errorf("GetFileColor(%q) returned White, expected colored output", ext)
		}
	}

	// Verify case variants return same color
	if GetFileColor(".GO") != GetFileColor(".go") {
		t.Error(".GO and .go should return same color")
	}
	if GetFileColor(".PY") != GetFileColor(".py") {
		t.Error(".PY and .py should return same color")
	}
}

func TestIsAssetExtension(t *testing.T) {
	assetExts := []string{
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
		".ttf", ".otf", ".woff", ".woff2",
		".mp3", ".wav", ".mp4", ".mov",
		".zip", ".tar", ".gz", ".7z",
		".pdf", ".doc", ".docx",
		".exe", ".dll", ".so", ".dylib",
		".lock", ".sum", ".map",
	}

	for _, ext := range assetExts {
		if !IsAssetExtension(ext) {
			t.Errorf("IsAssetExtension(%q) = false, want true", ext)
		}
	}

	// Test case insensitivity
	if !IsAssetExtension(".PNG") {
		t.Error("IsAssetExtension should be case-insensitive for .PNG")
	}
	if !IsAssetExtension(".Jpg") {
		t.Error("IsAssetExtension should be case-insensitive for .Jpg")
	}
}

func TestIsAssetExtensionSourceFiles(t *testing.T) {
	sourceExts := []string{
		".go", ".py", ".js", ".ts", ".rs", ".c", ".cpp",
		".java", ".swift", ".kt", ".rb", ".php", ".html", ".css",
	}

	for _, ext := range sourceExts {
		if IsAssetExtension(ext) {
			t.Errorf("IsAssetExtension(%q) = true, want false for source file", ext)
		}
	}
}

func TestCenterString(t *testing.T) {
	tests := []struct {
		s        string
		width    int
		expected string
	}{
		{"test", 10, "   test   "},
		{"test", 11, "   test    "},
		{"test", 4, "test"},
		{"test", 3, "test"}, // String longer than width
		{"ab", 6, "  ab  "},
		{"", 4, "    "},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := CenterString(tt.s, tt.width)
			if got != tt.expected {
				t.Errorf("CenterString(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.expected)
			}
		})
	}
}

func TestCenterStringLength(t *testing.T) {
	// Verify centered string has correct length when string fits
	s := "hello"
	width := 20
	centered := CenterString(s, width)

	if len(centered) != width {
		t.Errorf("CenterString(%q, %d) length = %d, want %d", s, width, len(centered), width)
	}

	// When string is longer than width, it should be unchanged
	s = "very long string"
	width = 5
	centered = CenterString(s, width)
	if centered != s {
		t.Errorf("CenterString should return original string when width < len(s)")
	}
}

func TestGetTerminalWidth(t *testing.T) {
	// This test just ensures GetTerminalWidth doesn't panic
	// and returns a reasonable value
	width := GetTerminalWidth()
	if width <= 0 {
		t.Errorf("GetTerminalWidth() = %d, want positive value", width)
	}
	// Default should be 80 when not running in a terminal
	// or the actual terminal width
	if width != 80 && width < 40 {
		t.Errorf("GetTerminalWidth() = %d, expected >= 40 or 80", width)
	}
}

func TestANSIConstants(t *testing.T) {
	// Verify ANSI constants are properly defined escape sequences
	constants := map[string]string{
		"Reset":     Reset,
		"Bold":      Bold,
		"Dim":       Dim,
		"White":     White,
		"Cyan":      Cyan,
		"Yellow":    Yellow,
		"Magenta":   Magenta,
		"Green":     Green,
		"Red":       Red,
		"Blue":      Blue,
		"BoldWhite": BoldWhite,
		"BoldRed":   BoldRed,
		"BoldBlue":  BoldBlue,
		"DimWhite":  DimWhite,
		"BoldGreen": BoldGreen,
	}

	for name, value := range constants {
		if !strings.HasPrefix(value, "\033[") {
			t.Errorf("%s should start with ANSI escape sequence, got %q", name, value)
		}
		if !strings.HasSuffix(value, "m") {
			t.Errorf("%s should end with 'm', got %q", name, value)
		}
	}
}
