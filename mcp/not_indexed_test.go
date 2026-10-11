package codemapmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/scanner"
)

// TestFileContextAndImportersNeverReportConfidentZerosForUnindexedFiles is
// the MCP half of #140: a made-up path, or a file codemap scanned but never
// parsed for imports, must be answered with the one reason it has no entry
// in the graph, never with "leaf file" / "entry point or unused" / "No files
// import". A scanned source file with zero importers keeps its answer.
func TestFileContextAndImportersNeverReportConfidentZerosForUnindexedFiles(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}
	root := t.TempDir()
	writeMCPImportersFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "NOTES.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	confident := []string{"leaf file", "entry point or unused", "No files import", "CONNECTED:"}
	for _, tc := range []struct {
		file   string
		reason string
	}{
		{"totally/made/up.go", "path does not exist"},
		{"NOTES.md", "unsupported file type: .md has no import analysis"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			wantLine := "not indexed: " + filepath.FromSlash(tc.file) + " is not in the scanned file set (" + tc.reason + ")"

			ctxRes, _, err := handleGetFileContext(context.Background(), nil, ImportersInput{Path: root, File: tc.file})
			if err != nil {
				t.Fatalf("handleGetFileContext error: %v", err)
			}
			ctxOut := resultText(t, ctxRes)
			if !strings.Contains(ctxOut, wantLine) || !strings.Contains(ctxOut, "Next: ") {
				t.Fatalf("file context lacks the not-indexed answer %q:\n%s", wantLine, ctxOut)
			}
			for _, claim := range confident {
				if strings.Contains(ctxOut, claim) {
					t.Fatalf("file context still claims %q for an unindexed file:\n%s", claim, ctxOut)
				}
			}

			impRes, structured, err := handleGetImporters(context.Background(), nil, ImportersInput{Path: root, File: tc.file})
			if err != nil {
				t.Fatalf("handleGetImporters error: %v", err)
			}
			impOut := resultText(t, impRes)
			if !strings.Contains(impOut, wantLine) || strings.Contains(impOut, "No files import") {
				t.Fatalf("importers text lacks the not-indexed answer %q:\n%s", wantLine, impOut)
			}
			out, ok := structured.(ImportersOutput)
			if !ok {
				t.Fatalf("structured content = %T, want ImportersOutput", structured)
			}
			if out.Kind != "not_indexed" || !out.NotIndexed || out.NotIndexedReason != tc.reason || out.ImporterCount != 0 {
				t.Fatalf("structured importers = %+v, want kind not_indexed with reason %q", out, tc.reason)
			}
		})
	}

	// a/a.go imports the hub and nothing imports it: a scanned source file
	// whose zero importers is a real count, so its answers are unchanged.
	ctxRes, _, err := handleGetFileContext(context.Background(), nil, ImportersInput{Path: root, File: "a/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out := resultText(t, ctxRes); !strings.Contains(out, "IMPORTED BY: none (entry point or unused)") || strings.Contains(out, "not indexed") {
		t.Fatalf("indexed file context changed:\n%s", out)
	}
	impRes, structured, err := handleGetImporters(context.Background(), nil, ImportersInput{Path: root, File: "a/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if out := resultText(t, impRes); !strings.Contains(out, "No files import 'a/a.go'") {
		t.Fatalf("indexed importers text changed:\n%s", out)
	}
	if out := structured.(ImportersOutput); out.Kind != "empty" || out.NotIndexed {
		t.Fatalf("indexed zero-importer file flagged not indexed: %+v", out)
	}
}
