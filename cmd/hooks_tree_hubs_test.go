package cmd

import "testing"

func TestTreeStatesHubs(t *testing.T) {
	if !treeStatesHubs("tree\n⭐️ ←N = imported by N files\n") || !treeStatesHubs("No ⭐️ hub marks: x") || treeStatesHubs("plain tree\n") {
		t.Fatal("hub legend detection wrong")
	}
}
