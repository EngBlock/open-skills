// Package state reads the existing project and global Open Skills state without
// mutating it. The package is internal because the supported interface is the
// open-skills process.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Scope string

const (
	Project Scope = "project"
	Global  Scope = "global"
)

type ErrorCode string

const (
	ErrorMalformed    ErrorCode = "state_malformed"
	ErrorOlderVersion ErrorCode = "state_version_older"
	ErrorNewerVersion ErrorCode = "state_version_newer"
	ErrorUnreadable   ErrorCode = "state_unreadable"
)

type InspectionError struct {
	Code            ErrorCode
	Path            string
	ExpectedVersion int
	FoundVersion    int
	Cause           error
}

func (failure *InspectionError) Error() string {
	if failure.Code == ErrorOlderVersion || failure.Code == ErrorNewerVersion {
		return fmt.Sprintf("%s uses schema version %d; this executable supports version %d", failure.Path, failure.FoundVersion, failure.ExpectedVersion)
	}
	return fmt.Sprintf("%s: %v", failure.Path, failure.Cause)
}

func (failure *InspectionError) Unwrap() error {
	return failure.Cause
}

type LockEntry struct {
	Source               string
	SourceURL            string
	SourceType           string
	Ref                  string
	SkillPath            string
	ComputedHash         string
	SkillFolderHash      string
	InstalledContentHash string
	PluginName           string
	Agents               []string
	Subagents            []string
	raw                  map[string]json.RawMessage
}

// InstallationRecord is the stable information recorded for a locally
// installed skill. It intentionally contains no credentials or project-scoped
// timestamps. Content identity and the owned file list let later mutating
// commands detect local changes without rediscovering the source.
type InstallationRecord struct {
	Source               string
	SourceURL            string
	SourceType           string
	Ref                  string
	SkillPath            string
	InstalledContentHash string
	SkillFolderHash      string
	OwnedFiles           []string
	// Agents records the selected adapter placements in stable registry order.
	Agents []string
	// Subagents records Eve placements; an empty value represents the root agent.
	Subagents []string
}

type Document struct {
	Version int
	Skills  map[string]LockEntry
	raw     map[string]json.RawMessage
}

type InstalledSkill struct {
	Name          string
	CanonicalPath string
	Scope         Scope
	Agents        []string
	Lock          *LockEntry
}

type Snapshot struct {
	LockPath string
	Lock     *Document
	Skills   []InstalledSkill
}

type InspectOptions struct {
	Scope         Scope
	Project       string
	Home          string
	XDGStateHome  string
	XDGConfigHome string
	AgentFilter   []string
}

func Inspect(options InspectOptions) (Snapshot, error) {
	lockPath := lockPath(options)
	lock, err := Read(lockPath, expectedVersion(options.Scope))
	if err != nil {
		return Snapshot{}, err
	}

	base := options.Project
	if options.Scope == Global {
		base = options.Home
	}
	canonicalDirectory := filepath.Join(base, ".agents", "skills")
	canonical, err := scanSkills(canonicalDirectory, options.Scope, lock)
	if err != nil {
		return Snapshot{}, err
	}

	byName := make(map[string]int, len(canonical))
	for index := range canonical {
		byName[canonical[index].Name] = index
	}
	claimedDirectories := make(map[string]bool)
	for _, agent := range orderedAgentConfigs(options) {
		for _, directory := range agentSkillDirectories(agent, options) {
			if samePath(directory, canonicalDirectory) {
				if agentDetected(agent, options) {
					for index := range canonical {
						appendAgent(&canonical[index], agent.ID)
					}
				}
				continue
			}
			directoryKey, err := filepath.Abs(directory)
			if err != nil {
				directoryKey = filepath.Clean(directory)
			}
			directoryKey = filepath.Clean(directoryKey)
			if claimedDirectories[directoryKey] {
				continue
			}
			claimedDirectories[directoryKey] = true
			agentSkills, err := scanSkills(directory, options.Scope, lock)
			if err != nil {
				continue
			}
			for _, skill := range agentSkills {
				if index, exists := byName[skill.Name]; exists {
					appendAgent(&canonical[index], agent.ID)
					continue
				}
				skill.Agents = []string{agent.ID}
				canonical = append(canonical, skill)
				byName[skill.Name] = len(canonical) - 1
			}
		}
	}
	for index := range canonical {
		sortAgentIDs(canonical[index].Agents)
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
	return Snapshot{LockPath: lockPath, Lock: lock, Skills: canonical}, nil
}

func Read(path string, expectedVersion int) (*Document, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Document{Version: expectedVersion, Skills: map[string]LockEntry{}}, nil
	}
	if err != nil {
		return nil, &InspectionError{Code: ErrorUnreadable, Path: path, ExpectedVersion: expectedVersion, Cause: err}
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		if err == nil {
			err = errors.New("state root must be an object")
		}
		return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: err}
	}
	var version int
	if err := json.Unmarshal(raw["version"], &version); err != nil {
		return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: errors.New("version must be an integer")}
	}
	if version < expectedVersion {
		return nil, &InspectionError{Code: ErrorOlderVersion, Path: path, ExpectedVersion: expectedVersion, FoundVersion: version}
	}
	if version > expectedVersion {
		return nil, &InspectionError{Code: ErrorNewerVersion, Path: path, ExpectedVersion: expectedVersion, FoundVersion: version}
	}
	var rawSkills map[string]json.RawMessage
	if err := json.Unmarshal(raw["skills"], &rawSkills); err != nil || rawSkills == nil {
		return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: errors.New("skills must be an object")}
	}
	if err := validateTopLevel(raw, expectedVersion); err != nil {
		return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: err}
	}
	document := &Document{Version: version, Skills: make(map[string]LockEntry, len(rawSkills)), raw: raw}
	for name, entryData := range rawSkills {
		if name == "" {
			return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: errors.New("skill name must not be empty")}
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(entryData, &fields); err != nil || fields == nil {
			return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: fmt.Errorf("skill %q must be an object", name)}
		}
		entry, err := decodeLockEntry(fields, expectedVersion)
		if err != nil {
			return nil, &InspectionError{Code: ErrorMalformed, Path: path, ExpectedVersion: expectedVersion, Cause: fmt.Errorf("skill %q: %w", name, err)}
		}
		document.Skills[name] = entry
	}
	return document, nil
}

func validateTopLevel(raw map[string]json.RawMessage, expectedVersion int) error {
	if expectedVersion == 3 {
		if value, exists := raw["dismissed"]; exists {
			var dismissed map[string]json.RawMessage
			if err := json.Unmarshal(value, &dismissed); err != nil || dismissed == nil {
				return errors.New("dismissed must be an object")
			}
			if prompt, exists := dismissed["findSkillsPrompt"]; exists {
				var enabled bool
				if err := json.Unmarshal(prompt, &enabled); err != nil {
					return errors.New("dismissed.findSkillsPrompt must be a boolean")
				}
			}
		}
		if value, exists := raw["lastSelectedAgents"]; exists {
			var agents []string
			if err := json.Unmarshal(value, &agents); err != nil {
				return errors.New("lastSelectedAgents must be an array of strings")
			}
		}
	}
	return nil
}

func decodeLockEntry(fields map[string]json.RawMessage, expectedVersion int) (LockEntry, error) {
	entry := LockEntry{raw: fields}
	var err error
	if entry.Source, err = requiredString(fields, "source"); err != nil {
		return LockEntry{}, err
	}
	if entry.SourceType, err = requiredString(fields, "sourceType"); err != nil {
		return LockEntry{}, err
	}
	if expectedVersion == 1 {
		if entry.ComputedHash, err = requiredString(fields, "computedHash"); err != nil {
			return LockEntry{}, err
		}
	} else {
		for _, name := range []string{"sourceUrl", "installedAt", "updatedAt"} {
			if _, err := requiredString(fields, name); err != nil {
				return LockEntry{}, err
			}
		}
		if entry.SkillFolderHash, err = stringField(fields, "skillFolderHash", true); err != nil {
			return LockEntry{}, err
		}
	}
	for _, name := range []string{"sourceUrl", "ref", "skillPath", "pluginName", "installedContentHash"} {
		value, exists := fields[name]
		if !exists {
			continue
		}
		var decoded string
		if err := json.Unmarshal(value, &decoded); err != nil {
			return LockEntry{}, fmt.Errorf("%s must be a string", name)
		}
		switch name {
		case "sourceUrl":
			entry.SourceURL = decoded
		case "ref":
			entry.Ref = decoded
		case "skillPath":
			entry.SkillPath = decoded
		case "pluginName":
			entry.PluginName = decoded
		case "installedContentHash":
			entry.InstalledContentHash = decoded
		}
	}
	for field, destination := range map[string]*[]string{
		"agents":    &entry.Agents,
		"subagents": &entry.Subagents,
	} {
		value, exists := fields[field]
		if !exists {
			continue
		}
		if err := json.Unmarshal(value, destination); err != nil {
			// v3 baseline state may use these names for unrelated extensions.
			// Preserve them verbatim unless they have the native array shape.
			if expectedVersion != 1 {
				*destination = nil
				continue
			}
			return LockEntry{}, fmt.Errorf("%s must be an array of strings", field)
		}
		for _, name := range *destination {
			if name == "" && field == "subagents" {
				continue
			}
			if strings.TrimSpace(name) == "" {
				return LockEntry{}, fmt.Errorf("%s must not contain empty names", field)
			}
		}
	}
	return entry, nil
}

func requiredString(fields map[string]json.RawMessage, name string) (string, error) {
	return stringField(fields, name, false)
}

func stringField(fields map[string]json.RawMessage, name string, allowEmpty bool) (string, error) {
	value, exists := fields[name]
	if !exists {
		return "", fmt.Errorf("%s is required", name)
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil || (!allowEmpty && decoded == "") {
		if allowEmpty {
			return "", fmt.Errorf("%s must be a string", name)
		}
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return decoded, nil
}

// Marshal returns a deterministic representation suitable for a later
// validated write. Unknown top-level and entry fields are retained verbatim as
// JSON values so supported extensions survive native rewrites.
func (document *Document) Marshal() ([]byte, error) {
	top := cloneRawMap(document.raw)
	version, err := json.Marshal(document.Version)
	if err != nil {
		return nil, err
	}
	top["version"] = version

	skills := make(map[string]json.RawMessage, len(document.Skills))
	for name, entry := range document.Skills {
		fields := cloneRawMap(entry.raw)
		for field, value := range map[string]string{
			"source": entry.Source, "sourceType": entry.SourceType,
		} {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			fields[field] = encoded
		}
		for field, value := range map[string]string{"sourceUrl": entry.SourceURL, "ref": entry.Ref, "skillPath": entry.SkillPath, "pluginName": entry.PluginName} {
			if value == "" {
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			fields[field] = encoded
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		skills[name] = encoded
	}
	encodedSkills, err := json.Marshal(skills)
	if err != nil {
		return nil, err
	}
	top["skills"] = encodedSkills
	encoded, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// RecordInstallation adds or replaces one supported lock entry while retaining
// unknown fields elsewhere in the document. Project records deliberately omit
// timestamps; the legacy global schema requires them, so global records add
// them only there.
func (document *Document) RecordInstallation(name string, record InstallationRecord) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("skill name must not be empty")
	}
	if strings.TrimSpace(record.Source) == "" || strings.TrimSpace(record.SourceType) == "" || strings.TrimSpace(record.InstalledContentHash) == "" {
		return errors.New("installation record is incomplete")
	}
	if document.raw == nil {
		document.raw = make(map[string]json.RawMessage)
	}
	record.Agents = normalizeAgentIDs(record.Agents)
	key, existing := document.EntryWithName(name)
	if existing == nil {
		key = name
	}
	entry := document.Skills[key]
	fields := cloneRawMap(entry.raw)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	setString := func(field, value string) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		fields[field] = encoded
		return nil
	}
	for field, value := range map[string]string{
		"source": record.Source, "sourceType": record.SourceType,
	} {
		if err := setString(field, value); err != nil {
			return err
		}
	}
	if document.Version == 1 {
		if err := setString("computedHash", record.InstalledContentHash); err != nil {
			return err
		}
		if record.SourceType != "git" && record.SourceType != "gitlab" && record.SourceType != "well-known" {
			delete(fields, "sourceUrl")
		} else if record.SourceURL != "" {
			if err := setString("sourceUrl", record.SourceURL); err != nil {
				return err
			}
		}
	} else {
		if err := setString("sourceUrl", record.SourceURL); err != nil {
			return err
		}
		folderHash := record.SkillFolderHash
		if folderHash == "" {
			folderHash = record.InstalledContentHash
		}
		if err := setString("skillFolderHash", folderHash); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, exists := fields["installedAt"]; !exists {
			if err := setString("installedAt", now); err != nil {
				return err
			}
		}
		if err := setString("updatedAt", now); err != nil {
			return err
		}
	}
	for field, value := range map[string]string{"ref": record.Ref, "skillPath": record.SkillPath} {
		if value == "" {
			delete(fields, field)
			continue
		}
		if err := setString(field, value); err != nil {
			return err
		}
	}
	if err := setString("installedContentHash", record.InstalledContentHash); err != nil {
		return err
	}
	owned, err := json.Marshal(record.OwnedFiles)
	if err != nil {
		return err
	}
	fields["ownedFiles"] = owned
	for field, values := range map[string][]string{
		"agents":    record.Agents,
		"subagents": record.Subagents,
	} {
		if len(values) == 0 {
			if document.Version == 1 {
				delete(fields, field)
			}
			continue
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return err
		}
		fields[field] = encoded
	}
	document.Skills[key] = LockEntry{
		Source: record.Source, SourceURL: record.SourceURL, SourceType: record.SourceType,
		Ref: record.Ref, SkillPath: record.SkillPath, ComputedHash: record.InstalledContentHash,
		SkillFolderHash: record.SkillFolderHash, InstalledContentHash: record.InstalledContentHash,
		Agents: record.Agents, Subagents: record.Subagents, raw: fields,
	}
	return nil
}

// Write stores a validated document atomically so a failed local install never
// leaves a partially written lock file.
func (document *Document) Write(path string) error {
	data, err := document.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".skills-lock-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for name, value := range source {
		result[name] = append(json.RawMessage(nil), value...)
	}
	return result
}

// EntryWithName resolves an installed name to the exact lock-file key. Lock
// keys retain the original name while on-disk paths use SanitizeName, so a
// remove operation needs both identities to update the correct entry.
func (document *Document) EntryWithName(name string) (string, *LockEntry) {
	if entry, ok := document.Skills[name]; ok {
		copy := entry
		return name, &copy
	}
	sanitized := sanitizeName(name)
	lockedNames := make([]string, 0, len(document.Skills))
	for lockedName := range document.Skills {
		lockedNames = append(lockedNames, lockedName)
	}
	sort.Strings(lockedNames)
	for _, lockedName := range lockedNames {
		if sanitizeName(lockedName) == sanitized {
			copy := document.Skills[lockedName]
			return lockedName, &copy
		}
	}
	return "", nil
}

func (document *Document) Entry(name string) *LockEntry {
	_, entry := document.EntryWithName(name)
	return entry
}

// RetainInstallationPlacements updates native project placement metadata after
// a partial removal. Older lock entries have no placement metadata, so their
// unknown shape is retained unchanged rather than inventing ownership claims.
func (document *Document) RetainInstallationPlacements(name string, agents, subagents []string) bool {
	key, entry := document.EntryWithName(name)
	if entry == nil || document.Version != 1 || len(entry.Agents) == 0 {
		return false
	}
	fields := cloneRawMap(entry.raw)
	agents = normalizeAgentIDs(agents)
	if len(agents) == 0 {
		delete(fields, "agents")
	} else if encoded, err := json.Marshal(agents); err == nil {
		fields["agents"] = encoded
	} else {
		return false
	}
	if len(subagents) == 0 {
		delete(fields, "subagents")
	} else if encoded, err := json.Marshal(subagents); err == nil {
		fields["subagents"] = encoded
	} else {
		return false
	}
	entry.Agents = agents
	entry.Subagents = append([]string(nil), subagents...)
	entry.raw = fields
	document.Skills[key] = *entry
	return true
}

// RemoveInstallation deletes the exact resolved lock entry after its final
// owned placement has been removed.
func (document *Document) RemoveInstallation(name string) bool {
	key, entry := document.EntryWithName(name)
	if entry == nil {
		return false
	}
	delete(document.Skills, key)
	return true
}

func orderedAgentConfigs(options InspectOptions) []agentConfig {
	selected := selectedAgentConfigs(options.AgentFilter)
	detected := make([]agentConfig, 0, len(selected))
	undetected := make([]agentConfig, 0, len(selected))
	for _, agent := range selected {
		if agentDetected(agent, options) {
			detected = append(detected, agent)
		} else {
			undetected = append(undetected, agent)
		}
	}
	return append(detected, undetected...)
}

func agentSkillDirectories(agent agentConfig, options InspectOptions) []string {
	directory := agentSkillsDir(agent, options)
	if directory == "" {
		return nil
	}
	result := []string{directory}
	if agent.ID != "eve" || options.Scope != Project || !agentDetected(agent, options) {
		return result
	}
	entries, err := os.ReadDir(filepath.Join(options.Project, "agent", "subagents"))
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if entry.IsDir() {
			result = append(result, filepath.Join(options.Project, "agent", "subagents", entry.Name(), "skills"))
		}
	}
	return result
}

func samePath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}

func appendAgent(skill *InstalledSkill, id string) {
	for _, existing := range skill.Agents {
		if existing == id {
			return
		}
	}
	skill.Agents = append(skill.Agents, id)
}

func lockPath(options InspectOptions) string {
	if options.Scope == Project {
		return filepath.Join(options.Project, "skills-lock.json")
	}
	if options.XDGStateHome != "" {
		return filepath.Join(options.XDGStateHome, "skills", ".skill-lock.json")
	}
	return filepath.Join(options.Home, ".agents", ".skill-lock.json")
}

func expectedVersion(scope Scope) int {
	if scope == Global {
		return 3
	}
	return 1
}

func scanSkills(directory string, scope Scope, lock *Document) ([]InstalledSkill, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []InstalledSkill{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect skills %s: %w", directory, err)
	}
	result := make([]InstalledSkill, 0, len(entries))
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.IsDir() {
			continue
		}
		name, ok := readSkillName(filepath.Join(directory, entry.Name(), "SKILL.md"))
		if !ok {
			continue
		}
		result = append(result, InstalledSkill{
			Name:          name,
			CanonicalPath: filepath.Join(directory, entry.Name()),
			Scope:         scope,
			Agents:        []string{},
			Lock:          lock.Entry(name),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func readSkillName(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return "", false
	}
	lines := strings.Split(string(data), "\n")
	var name, description string
	closed := false
	for index := 1; index < len(lines); index++ {
		line := lines[index]
		if line == "---" {
			closed = true
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "name" && key != "description" {
			continue
		}
		decoded, lastLine, valid := parseYAMLString(value, lines, index)
		if !valid {
			return "", false
		}
		index = lastLine
		if key == "name" {
			name = decoded
		} else {
			description = decoded
		}
	}
	return name, closed && name != "" && description != ""
}

func parseYAMLString(value string, lines []string, lineIndex int) (string, int, bool) {
	value = strings.TrimSpace(stripYAMLComment(value))
	if isYAMLBlockIndicator(value) {
		parts := []string{}
		lastLine := lineIndex
		for index := lineIndex + 1; index < len(lines); index++ {
			line := lines[index]
			if line == "" {
				parts = append(parts, "")
				lastLine = index
				continue
			}
			if line[0] != ' ' && line[0] != '\t' {
				break
			}
			parts = append(parts, strings.TrimSpace(line))
			lastLine = index
		}
		decoded := sanitizeMetadata(strings.Join(parts, "\n"))
		return decoded, lastLine, decoded != ""
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		decoded := strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		decoded = sanitizeMetadata(decoded)
		return decoded, lineIndex, decoded != ""
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", lineIndex, false
		}
		decoded = sanitizeMetadata(decoded)
		return decoded, lineIndex, decoded != ""
	}
	if !isYAMLPlainString(value) {
		return "", lineIndex, false
	}
	decoded := sanitizeMetadata(value)
	return decoded, lineIndex, decoded != ""
}

func isYAMLBlockIndicator(value string) bool {
	if value == "" || (value[0] != '|' && value[0] != '>') {
		return false
	}
	for _, char := range value[1:] {
		if char != '+' && char != '-' && (char < '1' || char > '9') {
			return false
		}
	}
	return true
}

func isYAMLPlainString(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	for _, keyword := range []string{"null", "~", "true", "false", ".nan", ".inf", "+.inf", "-.inf"} {
		if lower == keyword {
			return false
		}
	}
	if value[0] == '[' || value[0] == '{' || value[0] == '!' || value[0] == '&' || value[0] == '*' {
		return false
	}
	numeric := strings.ReplaceAll(value, "_", "")
	if _, err := strconv.ParseFloat(numeric, 64); err == nil {
		return false
	}
	if _, err := strconv.ParseInt(numeric, 0, 64); err == nil {
		return false
	}
	return true
}

func stripYAMLComment(value string) string {
	var quote byte
	escaped := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if quote == '"' && current == '\\' && !escaped {
			escaped = true
			continue
		}
		if (current == '\'' || current == '"') && !escaped {
			if quote == 0 {
				quote = current
			} else if quote == current {
				quote = 0
			}
		}
		if current == '#' && quote == 0 && index > 0 && (value[index-1] == ' ' || value[index-1] == '\t') {
			return value[:index]
		}
		escaped = false
	}
	return value
}

// SanitizeName returns the canonical on-disk directory name for a skill.
func SanitizeName(name string) string {
	return sanitizeName(name)
}

func sanitizeName(name string) string {
	var builder strings.Builder
	lastSeparator := false
	for _, char := range strings.ToLower(name) {
		allowed := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '.' || char == '_'
		if allowed {
			builder.WriteRune(char)
			lastSeparator = false
		} else if !lastSeparator {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	result := strings.Trim(builder.String(), ".-")
	if result == "" {
		return "unnamed-skill"
	}
	if len(result) > 255 {
		return result[:255]
	}
	return result
}

func sanitizeMetadata(value string) string {
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char == '\n' || char == '\r' || char == '\t':
			builder.WriteByte(' ')
		case char < 0x20 || char == 0x7f:
			continue
		default:
			builder.WriteRune(char)
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}
