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

func TestRecordInstallationWritesDeterministicTimestampFreeProjectState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills-lock.json")
	document, err := Read(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zeta", "alpha"} {
		if err := document.RecordInstallation(name, InstallationRecord{
			Source: "/source/" + name, SourceType: "local", InstalledContentHash: name + "-hash", OwnedFiles: []string{"SKILL.md"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := document.Write(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(data, []byte(`"alpha"`)) > bytes.Index(data, []byte(`"zeta"`)) {
		t.Fatalf("skill keys are not deterministic: %s", data)
	}
	if bytes.Contains(data, []byte("installedAt")) || bytes.Contains(data, []byte("updatedAt")) {
		t.Fatalf("project state includes timestamps: %s", data)
	}
	var output map[string]any
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	entry := output["skills"].(map[string]any)["alpha"].(map[string]any)
	if entry["installedContentHash"] != "alpha-hash" || !reflect.DeepEqual(entry["ownedFiles"], []any{"SKILL.md"}) {
		t.Fatalf("installation metadata = %#v", entry)
	}
}

func TestRecordInstallationReusesExistingSanitizedNameKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills-lock.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"skills":{"Case Skill":{"source":"/old","sourceType":"local","computedHash":"old","vendorExtension":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Read(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.RecordInstallation("case/skill", InstallationRecord{Source: "/new", SourceType: "local", InstalledContentHash: "new", OwnedFiles: []string{"SKILL.md"}}); err != nil {
		t.Fatal(err)
	}
	if len(document.Skills) != 1 {
		t.Fatalf("recording normalized name created duplicate keys: %#v", document.Skills)
	}
	entry, ok := document.Skills["Case Skill"]
	if !ok || entry.Source != "/new" || entry.ComputedHash != "new" {
		t.Fatalf("existing normalized key was not updated: %#v", document.Skills)
	}
	data, err := document.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"vendorExtension": true`)) {
		t.Fatalf("existing entry extensions were not retained: %s", data)
	}
}

func TestProjectStateReadsAndWritesRecordedPlacements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills-lock.json")
	document, err := Read(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.RecordInstallation("placed", InstallationRecord{
		Source: "local-source", SourceType: "local", InstalledContentHash: "hash", OwnedFiles: []string{"SKILL.md"},
		Agents: []string{"claude-code", "eve"}, Subagents: []string{"research"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := document.Write(path); err != nil {
		t.Fatal(err)
	}
	restored, err := Read(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	entry := restored.Entry("placed")
	if entry == nil || entry.ComputedHash != "hash" || !reflect.DeepEqual(entry.Agents, []string{"claude-code", "eve"}) || !reflect.DeepEqual(entry.Subagents, []string{"research"}) {
		t.Fatalf("restored placement = %#v", entry)
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

func TestRecordInstallationClearsStaleEveSubagents(t *testing.T) {
	document, err := Read(filepath.Join(t.TempDir(), "skills-lock.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.RecordInstallation("eve-skill", InstallationRecord{
		Source: "source", SourceType: "local", InstalledContentHash: "first", OwnedFiles: []string{"SKILL.md"}, Subagents: []string{"", "research"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := document.RecordInstallation("eve-skill", InstallationRecord{
		Source: "source", SourceType: "local", InstalledContentHash: "second", OwnedFiles: []string{"SKILL.md"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := document.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("subagents")) {
		t.Fatalf("stale Eve placement survived replacement: %s", data)
	}
}
