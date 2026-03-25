package cmd

import (
	"fmt"
	"os"
	"strings"

	"codemap/skills"
)

// RunSkill handles the "codemap skill" subcommand.
func RunSkill(args []string, root string) {
	subCmd := ""
	if len(args) > 0 {
		subCmd = args[0]
	}

	switch subCmd {
	case "list":
		runSkillList(root)
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: codemap skill show <name>")
			os.Exit(1)
		}
		runSkillShow(root, args[1])
	case "init":
		runSkillInit(root)
	default:
		fmt.Println("Usage: codemap skill <list|show|init>")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list          Show all available skills with descriptions")
		fmt.Println("  show <name>   Print full skill content")
		fmt.Println("  init          Create a SKILL.md template in .codemap/skills/")
	}
}

func runSkillList(root string) {
	idx, err := skills.LoadSkills(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading skills: %v\n", err)
		os.Exit(1)
	}

	if len(idx.Skills) == 0 {
		fmt.Println("No skills found.")
		return
	}

	fmt.Printf("Available skills (%d):\n\n", len(idx.Skills))
	for _, s := range idx.Skills {
		source := fmt.Sprintf("[%s]", s.Source)
		keywords := ""
		if len(s.Meta.Keywords) > 0 {
			keywords = fmt.Sprintf(" (%s)", strings.Join(s.Meta.Keywords, ", "))
		}
		fmt.Printf("  %-20s %-10s %s%s\n", s.Meta.Name, source, s.Meta.Description, keywords)
	}
}

func runSkillShow(root, name string) {
	idx, err := skills.LoadSkills(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading skills: %v\n", err)
		os.Exit(1)
	}

	skill, ok := idx.ByName[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Skill %q not found.\n", name)
		fmt.Fprintln(os.Stderr, "Run 'codemap skill list' to see available skills.")
		os.Exit(1)
	}

	fmt.Printf("# %s\n\n", skill.Meta.Name)
	fmt.Printf("Source: %s\n", skill.Source)
	if skill.Meta.Description != "" {
		fmt.Printf("Description: %s\n", skill.Meta.Description)
	}
	if len(skill.Meta.Keywords) > 0 {
		fmt.Printf("Keywords: %s\n", strings.Join(skill.Meta.Keywords, ", "))
	}
	if len(skill.Meta.Languages) > 0 {
		fmt.Printf("Languages: %s\n", strings.Join(skill.Meta.Languages, ", "))
	}
	fmt.Println()
	fmt.Println(skill.Body)
}

func runSkillInit(root string) {
	skillsDir := root + "/.codemap/skills"
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating skills directory: %v\n", err)
		os.Exit(1)
	}

	template := `---
name: my-skill
description: Describe when this skill should activate and what it does
priority: 5
keywords: ["keyword1", "keyword2"]
languages: ["go"]
---

# My Skill

Instructions for the AI agent when this skill is activated.

## When to use

Describe the situations where this skill applies.

## Steps

1. First step
2. Second step
3. Third step
`

	path := skillsDir + "/my-skill.md"
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Skill template already exists at %s\n", path)
		return
	}

	if err := os.WriteFile(path, []byte(template), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing template: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created skill template at %s\n", path)
	fmt.Println("Edit the file to define your custom skill.")
	fmt.Println("Run 'codemap skill list' to verify it loads.")
}
