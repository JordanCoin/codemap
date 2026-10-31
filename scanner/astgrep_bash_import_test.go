package scanner

import "testing"

// Shell `source` lines feed the import list only as literal relative paths:
// a variable, a command substitution or an absolute path is not a project
// file and must not become an unresolved entry (#154 part 5).
func TestExtractImportPathBashSourceForms(t *testing.T) {
	cases := map[string]string{
		"source ./b.sh":          "./b.sh",
		`source "./c.sh"`:        "./c.sh",
		"source 'lib/d.sh'":      "lib/d.sh",
		". ./b.sh":               "./b.sh",
		`. "$X"`:                 "",
		`source "$SOME_VAR"`:     "",
		"source $HOME/x.sh":      "",
		"source /etc/profile":    "",
		"source $(dirname $0)/a": "",
		"source":                 "",
	}
	for text, want := range cases {
		if got := extractImportPath(text); got != want {
			t.Errorf("extractImportPath(%q) = %q, want %q", text, got, want)
		}
	}
}
