package compatibility

import (
	"bytes"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

var ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

func CompareObservations(oracle, native Observation, normalization Normalization) []Difference {
	left := normalizedObservation(oracle, normalization)
	right := normalizedObservation(native, normalization)
	differences := make([]Difference, 0)
	compare := func(field string, oracleValue, nativeValue any) {
		if !reflect.DeepEqual(oracleValue, nativeValue) {
			differences = append(differences, Difference{Field: field, Oracle: oracleValue, Native: nativeValue})
		}
	}
	compare("stdout", left.Stdout, right.Stdout)
	compare("stderr", left.Stderr, right.Stderr)
	compare("exit code", left.ExitCode, right.ExitCode)
	compare("timeout", left.TimedOut, right.TimedOut)
	compare("process error", left.ProcessError, right.ProcessError)
	compare("filesystem", left.Files, right.Files)
	compare("locks", left.Locks, right.Locks)
	compare("lock parse errors", left.LockParseErrors, right.LockParseErrors)
	compare("HTTP requests", left.HTTPRequests, right.HTTPRequests)
	compare("spawned commands", left.SpawnedCommands, right.SpawnedCommands)
	return differences
}

func normalizedObservation(observation Observation, normalization Normalization) Observation {
	replacements := []Replacement{
		{Pattern: observation.Paths.Project, With: "<project>"},
		{Pattern: observation.ResolvedPaths.Project, With: "<project>"},
		{Pattern: observation.Paths.Home, With: "<home>"},
		{Pattern: observation.ResolvedPaths.Home, With: "<home>"},
		{Pattern: observation.Paths.Temp, With: "<tmp>"},
		{Pattern: observation.ResolvedPaths.Temp, With: "<tmp>"},
		{Pattern: observation.Paths.FixtureURL, With: "<fixture-url>"},
		{Pattern: observation.Paths.Root, With: "<sandbox>"},
		{Pattern: observation.ResolvedPaths.Root, With: "<sandbox>"},
	}
	replacements = append(replacements, normalization.Replacements...)
	sort.SliceStable(replacements, func(i, j int) bool { return len(replacements[i].Pattern) > len(replacements[j].Pattern) })
	normalizeText := func(value string) string {
		value = strings.ReplaceAll(value, "\r\n", "\n")
		for _, replacement := range replacements {
			if replacement.Pattern != "" {
				value = strings.ReplaceAll(value, replacement.Pattern, replacement.With)
			}
		}
		return value
	}
	normalizePresentation := func(value string) string {
		return ansiEscape.ReplaceAllString(normalizeText(value), "")
	}

	textFiles := make(map[string]struct{}, len(normalization.TextFiles))
	for _, path := range normalization.TextFiles {
		textFiles[path] = struct{}{}
	}
	result := observation
	result.Stdout = normalizePresentation(observation.Stdout)
	result.Stderr = normalizePresentation(observation.Stderr)
	result.ProcessError = normalizePresentation(observation.ProcessError)
	result.Paths = SandboxPaths{}
	result.ResolvedPaths = SandboxPaths{}
	result.Files = make(map[string]FileState, len(observation.Files))
	for path, state := range observation.Files {
		copy := state
		if _, ok := textFiles[path]; ok && copy.Kind == FileKindRegular {
			copy.Data = []byte(normalizeText(string(copy.Data)))
		}
		if copy.Kind == FileKindSymlink {
			copy.LinkTarget = normalizeText(copy.LinkTarget)
		}
		result.Files[path] = copy
	}
	result.Locks = make(map[LockLocation][]byte, len(observation.Locks))
	for location, data := range observation.Locks {
		result.Locks[location] = []byte(normalizeText(string(data)))
	}
	result.ParsedLocks = nil
	result.LockParseErrors = make(map[LockLocation]string, len(observation.LockParseErrors))
	for location, parseError := range observation.LockParseErrors {
		result.LockParseErrors[location] = normalizeText(parseError)
	}
	result.SpawnedCommands = append([]SpawnedCommand{}, observation.SpawnedCommands...)
	for index := range result.SpawnedCommands {
		result.SpawnedCommands[index].Cwd = normalizeText(result.SpawnedCommands[index].Cwd)
		result.SpawnedCommands[index].Args = append([]string{}, observation.SpawnedCommands[index].Args...)
		for argument := range result.SpawnedCommands[index].Args {
			result.SpawnedCommands[index].Args[argument] = normalizeText(result.SpawnedCommands[index].Args[argument])
		}
	}
	result.HTTPRequests = cloneRequests(observation.HTTPRequests)
	fixtureHost := ""
	if parsed, err := url.Parse(observation.Paths.FixtureURL); err == nil {
		fixtureHost = parsed.Host
	}
	for index := range result.HTTPRequests {
		if result.HTTPRequests[index].Host == fixtureHost {
			result.HTTPRequests[index].Host = "<fixture-host>"
		}
	}
	return result
}

func cloneRequests(requests []HTTPRequest) []HTTPRequest {
	result := make([]HTTPRequest, len(requests))
	for index, request := range requests {
		result[index] = request
		result[index].Header = request.Header.Clone()
		result[index].Body = bytes.Clone(request.Body)
	}
	return result
}
