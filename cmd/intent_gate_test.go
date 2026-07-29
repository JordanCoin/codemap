package cmd

import (
	"strings"
	"testing"

	"codemap/config"
)

func TestClassifyIntentDetectsVCSWorkflow(t *testing.T) {
	prompts := []string{
		"help me finish all open prs. start with the other people ones and merge those in",
		"review the open pull request and land it",
		"rebase this branch and push it",
		"cut a release and tag it",
	}
	for _, prompt := range prompts {
		intent := classifyIntent(prompt, nil, nil, config.ProjectConfig{})
		if intent.Category != "vcs" {
			t.Fatalf("classifyIntent(%q).Category = %q, want vcs", prompt, intent.Category)
		}
		if intent.Confidence < intentConfidenceFloor {
			t.Fatalf("classifyIntent(%q).Confidence = %v, want >= %v", prompt, intent.Confidence, intentConfidenceFloor)
		}
	}
}

func TestClassifyIntentVCSDoesNotHijackCodePrompts(t *testing.T) {
	intent := classifyIntent("fix the broken debounce bug in the watch daemon", nil, nil, config.ProjectConfig{})
	if intent.Category != "bugfix" {
		t.Fatalf("Category = %q, want bugfix", intent.Category)
	}
}

func TestPlanCodemapNextStepsForVCSSuggestsDiffNotEditAdvice(t *testing.T) {
	intent := TaskIntent{Category: "vcs", Confidence: 1}
	steps := planCodemapNextSteps(intent, nil)

	var commands []string
	for _, step := range steps {
		commands = append(commands, step.Command)
	}
	joined := strings.Join(commands, "\n")
	if strings.Contains(joined, "codemap --deps") || joined == "codemap ." {
		t.Fatalf("VCS workflow got edit-flavored advice: %v", commands)
	}
	if !strings.Contains(joined, "codemap --diff") {
		t.Fatalf("VCS workflow should suggest reviewing the diff, got: %v", commands)
	}
}

func TestPromptSubmitSuppressesZeroConfidenceIntent(t *testing.T) {
	root := t.TempDir()

	// No category signal words at all: classifier falls back with confidence 0.
	input := `{"prompt":"hello there, whats up my friend", "session_id":"gate-test"}`
	withStdinInput(t, input, func() {
		out := captureOutput(func() {
			if err := hookPromptSubmit(root); err != nil {
				t.Fatalf("hookPromptSubmit: %v", err)
			}
		})
		if strings.Contains(out, "codemap:intent") {
			t.Fatalf("zero-confidence intent marker should be suppressed, got:\n%s", out)
		}
		if strings.Contains(out, "Next codemap:") {
			t.Fatalf("zero-confidence next steps should be suppressed, got:\n%s", out)
		}
	})
}

func TestPromptSubmitEmitsConfidentIntent(t *testing.T) {
	root := t.TempDir()

	input := `{"prompt":"fix the broken parser bug", "session_id":"gate-test"}`
	withStdinInput(t, input, func() {
		out := captureOutput(func() {
			if err := hookPromptSubmit(root); err != nil {
				t.Fatalf("hookPromptSubmit: %v", err)
			}
		})
		if !strings.Contains(out, "codemap:intent") {
			t.Fatalf("confident intent should emit a marker, got:\n%s", out)
		}
	})
}
