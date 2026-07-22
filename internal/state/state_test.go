package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGlobalStateAcceptsEmptyFolderHashAndPreservesUnknownFields(t *testing.T) {
	original := []byte(`{
  "version": 3,
  "skills": {
    "well-known": {
      "source": "example.com",
      "sourceType": "well-known",
      "sourceUrl": "https://example.com/skill.md",
      "skillFolderHash": "",
      "installedAt": "2026-01-01T00:00:00.000Z",
      "updatedAt": "2026-01-01T00:00:00.000Z",
      "subagents": {"safeGlobalExtension": true}
    }
  }
}`)
	path := filepath.Join(t.TempDir(), ".skill-lock.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Read(path, 3)
	if err != nil {
		t.Fatalf("valid global state was rejected: %v", err)
	}
	roundTrip, err := document.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(roundTrip, &output); err != nil {
		t.Fatal(err)
	}
	entry := output["skills"].(map[string]any)["well-known"].(map[string]any)
	if !reflect.DeepEqual(entry["subagents"], map[string]any{"safeGlobalExtension": true}) {
		t.Fatalf("global extension changed: %#v", entry["subagents"])
	}
}

func TestSkillFrontmatterRejectsNonStringNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: 123\ndescription: invalid name\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if name, ok := readSkillName(path); ok {
		t.Fatalf("non-string YAML name was accepted as %q", name)
	}
}

func TestSkillFrontmatterDecodesQuotedAndFoldedScalars(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "double quoted escape", data: "---\nname: \"my\\u002dskill\"\ndescription: quoted\n---\n", want: "my-skill"},
		{name: "folded", data: "---\nname: >-\n  folded-skill\ndescription: folded\n---\n", want: "folded-skill"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			name, ok := readSkillName(path)
			if !ok || name != test.want {
				t.Fatalf("frontmatter name = %q, %v; want %q", name, ok, test.want)
			}
		})
	}
}

func TestSkillFrontmatterUsesYAMLCommentSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("---\r\nname: documented-name # ordinary comment\r\ndescription: valid description\r\n---\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, ok := readSkillName(path)
	if !ok || name != "documented-name" {
		t.Fatalf("frontmatter name = %q, %v", name, ok)
	}
}

func TestSupportedStateRoundTripPreservesSafeUnknownFields(t *testing.T) {
	original := []byte(`{
  "version": 1,
  "skills": {
    "upstream-project": {
      "source": "vercel-labs/skills",
      "sourceType": "github",
      "computedHash": "upstream-project-hash",
      "vendorExtension": {"retained": true}
    }
  },
  "upstreamMetadata": {"fixtureVersion": "v1.5.20"}
}
`)
	path := filepath.Join(t.TempDir(), "skills-lock.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Read(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := document.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal(original, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(roundTrip, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before["upstreamMetadata"], after["upstreamMetadata"]) {
		t.Fatalf("top-level extension changed: before %#v after %#v", before["upstreamMetadata"], after["upstreamMetadata"])
	}
	beforeEntry := before["skills"].(map[string]any)["upstream-project"].(map[string]any)
	afterEntry := after["skills"].(map[string]any)["upstream-project"].(map[string]any)
	if !reflect.DeepEqual(beforeEntry["vendorExtension"], afterEntry["vendorExtension"]) {
		t.Fatalf("entry extension changed: before %#v after %#v", beforeEntry["vendorExtension"], afterEntry["vendorExtension"])
	}
	if !bytes.HasSuffix(roundTrip, []byte("\n")) {
		t.Fatalf("round-trip output lacks final newline: %q", roundTrip)
	}
}
