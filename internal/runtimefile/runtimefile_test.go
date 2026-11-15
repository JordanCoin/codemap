package runtimefile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicWithPreservesDestinationAfterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("encode failed")
	err := WriteAtomicWith(path, 0o644, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("destination = %q, want old", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory entries = %d, want 1", len(entries))
	}
}

func TestWriteAtomicWithReplacesDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomicWith(path, 0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, "new")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want new", data)
	}
}

// Every state write and read refuses a planted symlink, leaving its target
// untouched.
func TestEveryStateAccessRefusesAPlantedLink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "state.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if f, err := OpenAppend(link, 0o600); err == nil {
		_, _ = f.Write([]byte("leak\n"))
		_ = f.Close()
		t.Error("OpenAppend followed the link")
	}
	if err := WriteAtomic(link, []byte("leak\n"), 0o600); err == nil {
		t.Error("WriteAtomic replaced the link")
	}
	if _, err := Read(link); err == nil {
		t.Error("Read followed the link")
	}
	if f, err := OpenRead(link); err == nil {
		_ = f.Close()
		t.Error("OpenRead followed the link")
	}
	if data, _ := os.ReadFile(outside); string(data) != "mine\n" {
		t.Fatalf("outside file changed: %q", data)
	}
}
