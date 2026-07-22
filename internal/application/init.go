package application

import (
	"fmt"
	"os"
	"path/filepath"
)

func runInit(invocation Invocation, arguments []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		return initFailure(invocation, err)
	}

	hasName := len(arguments) > 0
	skillName := filepath.Base(cwd)
	skillDirectory := cwd
	displayPath := "SKILL.md"
	if hasName {
		if arguments[0] != "" {
			skillName = arguments[0]
		}
		skillDirectory = filepath.Join(cwd, skillName)
		displayPath = skillName + "/SKILL.md"
	}
	skillFile := filepath.Join(skillDirectory, "SKILL.md")

	if _, err := os.Stat(skillFile); err == nil {
		_, _ = fmt.Fprintf(invocation.Stdout, "Skill already exists at %s\n", displayPath)
		return 0
	} else if !os.IsNotExist(err) {
		return initFailure(invocation, err)
	}

	if hasName {
		if err := os.MkdirAll(skillDirectory, 0o755); err != nil {
			return initFailure(invocation, err)
		}
	}

	content := fmt.Sprintf(`---
name: %s
description: A brief description of what this skill does
---

# %s

Instructions for the agent to follow when this skill is activated.

## When to use

Describe when this skill should be used.

## Instructions

1. First step
2. Second step
3. Additional steps as needed
`, skillName, skillName)
	if err := os.WriteFile(skillFile, []byte(content), 0o666); err != nil {
		return initFailure(invocation, err)
	}

	_, _ = fmt.Fprintf(invocation.Stdout, `Initialized skill: %s

Created:
  %s

Next steps:
  1. Edit %s to define your skill instructions
  2. Update the name and description in the frontmatter

Publishing:
  GitHub:  Push to a repo, then open-skills add <owner>/<repo>
  URL:     Host the file, then open-skills add https://example.com/%s

`, skillName, displayPath, displayPath, displayPath)
	return 0
}

func initFailure(invocation Invocation, err error) int {
	_, _ = fmt.Fprintf(invocation.Stderr, "Failed to initialize skill: %v\n", err)
	return 1
}
