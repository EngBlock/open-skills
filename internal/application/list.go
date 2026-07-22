package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/EngBlock/open-skills/internal/state"
)

type listOptions struct {
	Global bool
	JSON   bool
	Agents []string
}

type listJSONOutput struct {
	SchemaVersion int             `json:"schemaVersion"`
	Scope         state.Scope     `json:"scope"`
	Skills        []listJSONSkill `json:"skills"`
}

type listJSONErrorOutput struct {
	SchemaVersion int           `json:"schemaVersion"`
	Error         listJSONError `json:"error"`
}

type listJSONError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type listJSONSkill struct {
	Name       string      `json:"name"`
	Path       string      `json:"path"`
	Scope      state.Scope `json:"scope"`
	Agents     []string    `json:"agents"`
	Source     *string     `json:"source"`
	SourceURL  *string     `json:"sourceUrl"`
	SourceType *string     `json:"sourceType"`
}

func runList(invocation Invocation, arguments []string) int {
	options := parseListOptions(arguments)
	validAgents := make(map[string]bool)
	for _, id := range state.AgentIDs() {
		validAgents[id] = true
	}
	invalidAgents := []string{}
	for _, id := range options.Agents {
		if !validAgents[id] {
			invalidAgents = append(invalidAgents, id)
		}
	}
	if len(invalidAgents) > 0 {
		message := fmt.Sprintf("Invalid agents: %s", strings.Join(invalidAgents, ", "))
		if options.JSON {
			writeListJSONError(invocation, "invalid_agent", message, "")
		}
		_, _ = fmt.Fprintln(invocation.Stderr, message)
		_, _ = fmt.Fprintf(invocation.Stderr, "Valid agents: %s\n", strings.Join(state.AgentIDs(), ", "))
		return 1
	}
	project, err := os.Getwd()
	if err != nil {
		return listFailure(invocation, options.JSON, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return listFailure(invocation, options.JSON, err)
	}
	scope := state.Project
	if options.Global {
		scope = state.Global
	}
	snapshot, err := state.Inspect(state.InspectOptions{
		Scope:         scope,
		Project:       project,
		Home:          home,
		XDGStateHome:  os.Getenv("XDG_STATE_HOME"),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		AgentFilter:   options.Agents,
	})
	if err != nil {
		return listFailure(invocation, options.JSON, err)
	}
	if options.JSON {
		output := listJSONOutput{SchemaVersion: 1, Scope: scope, Skills: make([]listJSONSkill, 0, len(snapshot.Skills))}
		for _, skill := range snapshot.Skills {
			item := listJSONSkill{Name: skill.Name, Path: skill.CanonicalPath, Scope: skill.Scope, Agents: displayAgentNames(skill.Agents)}
			if skill.Lock != nil {
				if skill.Lock.Source != "" {
					item.Source = &skill.Lock.Source
				}
				if skill.Lock.SourceURL != "" {
					item.SourceURL = &skill.Lock.SourceURL
				}
				if skill.Lock.SourceType != "" {
					item.SourceType = &skill.Lock.SourceType
				}
			}
			output.Skills = append(output.Skills, item)
		}
		encoder := json.NewEncoder(invocation.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Failed to write list JSON: %v\n", err)
			return 1
		}
		return 0
	}

	if len(snapshot.Skills) == 0 {
		if scope == state.Global {
			_, _ = fmt.Fprintln(invocation.Stdout, "No global skills found.")
			_, _ = fmt.Fprintln(invocation.Stdout, "Try listing project skills without -g")
		} else {
			_, _ = fmt.Fprintln(invocation.Stdout, "No project skills found.")
			_, _ = fmt.Fprintln(invocation.Stdout, "Try listing global skills with -g")
		}
		return 0
	}

	scopeLabel := "Project"
	if scope == state.Global {
		scopeLabel = "Global"
	}
	_, _ = fmt.Fprintf(invocation.Stdout, "%s Skills\n", scopeLabel)
	_, _ = fmt.Fprintln(invocation.Stdout)
	groups := make(map[string][]state.InstalledSkill)
	ungrouped := []state.InstalledSkill{}
	for _, skill := range snapshot.Skills {
		if skill.Lock != nil && skill.Lock.PluginName != "" {
			groups[skill.Lock.PluginName] = append(groups[skill.Lock.PluginName], skill)
		} else {
			ungrouped = append(ungrouped, skill)
		}
	}
	if len(groups) == 0 {
		for _, skill := range ungrouped {
			writeHumanSkill(invocation, skill, scope, project, home, false)
		}
		_, _ = fmt.Fprintln(invocation.Stdout)
		return 0
	}
	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, name := range groupNames {
		_, _ = fmt.Fprintln(invocation.Stdout, pluginTitle(name))
		for _, skill := range groups[name] {
			writeHumanSkill(invocation, skill, scope, project, home, true)
		}
		_, _ = fmt.Fprintln(invocation.Stdout)
	}
	if len(ungrouped) > 0 {
		_, _ = fmt.Fprintln(invocation.Stdout, "General")
		for _, skill := range ungrouped {
			writeHumanSkill(invocation, skill, scope, project, home, true)
		}
		_, _ = fmt.Fprintln(invocation.Stdout)
	}
	return 0
}

func writeHumanSkill(invocation Invocation, skill state.InstalledSkill, scope state.Scope, project, home string, indent bool) {
	path := skill.CanonicalPath
	if scope == state.Global {
		if relative, ok := relativeWithin(home, path); ok {
			path = "~" + string(filepath.Separator) + relative
		}
	} else if relative, ok := relativeWithin(project, path); ok {
		path = "." + string(filepath.Separator) + relative
	}
	source := "local"
	if skill.Lock != nil && skill.Lock.Source != "" {
		source = sanitizeHuman(skill.Lock.Source)
	}
	agents := displayAgentNames(skill.Agents)
	agentText := "not linked"
	if len(agents) > 0 {
		agentText = formatAgentNames(agents)
	}
	prefix := ""
	if indent {
		prefix = "  "
	}
	_, _ = fmt.Fprintf(invocation.Stdout, "%s%s %s\n", prefix, sanitizeHuman(skill.Name), sanitizeHuman(path))
	_, _ = fmt.Fprintf(invocation.Stdout, "%s  Agents: %s  Source: %s\n", prefix, agentText, source)
}

func relativeWithin(base, target string) (string, bool) {
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func pluginTitle(name string) string {
	words := strings.Split(sanitizeHuman(name), "-")
	for index, word := range words {
		runes := []rune(word)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			words[index] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

func sanitizeHuman(value string) string {
	var builder strings.Builder
	for index := 0; index < len(value); {
		if value[index] == 0x1b {
			index++
			if index >= len(value) {
				break
			}
			switch value[index] {
			case '[':
				index++
				for index < len(value) {
					current := value[index]
					index++
					if current >= 0x40 && current <= 0x7e {
						break
					}
				}
			case ']':
				index++
				for index < len(value) {
					if value[index] == 0x07 {
						index++
						break
					}
					if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
						index += 2
						break
					}
					index++
				}
			default:
				index++
			}
			continue
		}
		current := value[index]
		index++
		if current == '\n' || current == '\r' || current == '\t' {
			builder.WriteByte(' ')
		} else if current >= 0x20 && current != 0x7f {
			builder.WriteByte(current)
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func parseListOptions(arguments []string) listOptions {
	options := listOptions{}
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "-g", "--global":
			options.Global = true
		case "--json":
			options.JSON = true
		case "-a", "--agent":
			for index+1 < len(arguments) && !strings.HasPrefix(arguments[index+1], "-") {
				index++
				options.Agents = append(options.Agents, arguments[index])
			}
		}
	}
	return options
}

func displayAgentNames(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, state.AgentDisplayName(id))
	}
	return result
}

func formatAgentNames(names []string) string {
	if len(names) <= 5 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(names[:5], ", "), len(names)-5)
}

func listFailure(invocation Invocation, jsonMode bool, err error) int {
	code := string(state.ErrorUnreadable)
	path := ""
	var failure *state.InspectionError
	if errors.As(err, &failure) {
		code = string(failure.Code)
		path = failure.Path
	}
	message := fmt.Sprintf("Failed to inspect skill state: %v", err)
	if jsonMode {
		writeListJSONError(invocation, code, message, path)
	}
	_, _ = fmt.Fprintln(invocation.Stderr, message)
	if path != "" {
		_, _ = fmt.Fprintln(invocation.Stderr, "The state file was not changed.")
		switch failure.Code {
		case state.ErrorOlderVersion:
			_, _ = fmt.Fprintf(invocation.Stderr, "Please back up %s and migrate it with a compatible Open Skills release before retrying.\n", path)
		case state.ErrorNewerVersion:
			_, _ = fmt.Fprintf(invocation.Stderr, "Please use an Open Skills release that supports schema version %d; this executable refuses to downgrade %s.\n", failure.FoundVersion, path)
		default:
			_, _ = fmt.Fprintf(invocation.Stderr, "Please back up %s, then restore a valid state file or move it aside before retrying.\n", path)
		}
	}
	return 1
}

func writeListJSONError(invocation Invocation, code, message, path string) {
	output := listJSONErrorOutput{SchemaVersion: 1, Error: listJSONError{Code: code, Message: message, Path: path}}
	encoder := json.NewEncoder(invocation.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(output)
}
