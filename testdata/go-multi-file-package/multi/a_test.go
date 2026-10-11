package multi

import "testing"

// A test file lives in the package but is never an import target.
func TestA(t *testing.T) {
	if A() != 1 {
		t.Fatal("A() != 1")
	}
}
