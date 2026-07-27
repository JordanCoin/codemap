package cmd

import (
	"context"
	"errors"
	"testing"
)

func TestRunMCPReturnsServerStatus(t *testing.T) {
	if code := runMCP(func(context.Context) error { return nil }); code != 0 {
		t.Fatalf("RunMCP success exit code = %d, want 0", code)
	}

	if code := runMCP(func(context.Context) error { return errors.New("boom") }); code != 1 {
		t.Fatalf("RunMCP failure exit code = %d, want 1", code)
	}
}
