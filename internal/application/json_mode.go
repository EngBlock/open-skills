package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

const automationSchemaVersion = 1

type automationExecution struct {
	output  any
	failure *automationJSONError
	trust   bool
}

type automationJSONError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type automationFailure struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Error         automationJSONError `json:"error"`
}

type trustAutomationFailure struct {
	Version int                 `json:"version"`
	Error   automationJSONError `json:"error"`
}

type codedAutomationError struct {
	code string
	err  error
}

func (failure *codedAutomationError) Error() string { return failure.err.Error() }
func (failure *codedAutomationError) Unwrap() error { return failure.err }

func automationError(code, message string) error {
	return &codedAutomationError{code: code, err: errors.New(message)}
}

func recordAutomationSuccess(invocation Invocation, output any) {
	if invocation.automation != nil {
		invocation.automation.output = output
	}
}

func recordAutomationFailure(invocation Invocation, code, message, path string) {
	if invocation.automation != nil && invocation.automation.failure == nil {
		invocation.automation.failure = &automationJSONError{Code: code, Message: sanitizeHuman(message), Path: path}
	}
}

func recordAutomationError(invocation Invocation, err error, fallbackCode string) {
	code := fallbackCode
	var coded *codedAutomationError
	if errors.As(err, &coded) {
		code = coded.code
	}
	recordAutomationFailure(invocation, code, err.Error(), "")
}

func stateAutomationError(err error, message string) automationJSONError {
	result := automationJSONError{Code: string(state.ErrorUnreadable), Message: sanitizeHuman(message)}
	var failure *state.InspectionError
	if errors.As(err, &failure) {
		result.Code = string(failure.Code)
		result.Path = failure.Path
	}
	return result
}

func recordStateOrAutomationError(invocation Invocation, err error, fallbackCode string) {
	var failure *state.InspectionError
	if errors.As(err, &failure) {
		result := stateAutomationError(err, err.Error())
		recordAutomationFailure(invocation, result.Code, result.Message, result.Path)
		return
	}
	recordAutomationError(invocation, err, fallbackCode)
}

func orderedAgentIDs(ids []string) []string {
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	ordered := make([]string, 0, len(selected))
	for _, id := range state.AgentIDs() {
		if selected[id] {
			ordered = append(ordered, id)
		}
	}
	return ordered
}

func normalizeJSONArguments(arguments []string) ([]string, bool) {
	normalized := make([]string, 0, len(arguments))
	jsonMode := false
	for _, argument := range arguments {
		if argument == "--json" {
			jsonMode = true
			continue
		}
		normalized = append(normalized, argument)
	}
	return normalized, jsonMode
}

func supportsJSON(arguments []string) bool {
	if len(arguments) == 0 {
		return false
	}
	switch arguments[0] {
	case "list", "ls", "check", "update", "upgrade", "add", "a", "remove", "rm", "r":
		return true
	case "install", "i":
		return len(arguments) > 1
	case "trust":
		return len(arguments) > 1 && (arguments[1] == "list" || arguments[1] == "ls")
	default:
		return false
	}
}

func runJSON(ctxInvocation Invocation, run func(Invocation) int) int {
	stdout := ctxInvocation.Stdout
	var suppressed bytes.Buffer
	ctxInvocation.Stdout = &suppressed
	ctxInvocation.Interactive = false
	ctxInvocation.JSON = true
	ctxInvocation.automation = &automationExecution{trust: len(ctxInvocation.Args) > 0 && ctxInvocation.Args[0] == "trust"}

	if !supportsJSON(ctxInvocation.Args) {
		message := "JSON mode is supported only for list, check, add, remove, update, and trust list"
		_, _ = fmt.Fprintln(ctxInvocation.Stderr, message)
		recordAutomationFailure(ctxInvocation, "json_not_supported", message, "")
		return writeAutomationResult(ctxInvocation, stdout, 1, suppressed.Bytes())
	}
	if hasHelpFlag(ctxInvocation.Args[1:]) {
		message := "Help output is not available in JSON mode"
		_, _ = fmt.Fprintln(ctxInvocation.Stderr, message)
		recordAutomationFailure(ctxInvocation, "json_not_supported", message, "")
		return writeAutomationResult(ctxInvocation, stdout, 1, suppressed.Bytes())
	}

	status := run(ctxInvocation)
	return writeAutomationResult(ctxInvocation, stdout, status, suppressed.Bytes())
}

func writeAutomationResult(invocation Invocation, stdout io.Writer, status int, captured []byte) int {
	// List and trust list had versioned JSON contracts before the global mode.
	// Preserve their exact successful payloads when a handler already produced
	// one complete document.
	if invocation.automation.output == nil && invocation.automation.failure == nil && singleJSONDocument(captured) {
		if _, err := stdout.Write(captured); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Write JSON result: %v\n", err)
			return 1
		}
		return status
	}

	if invocation.automation.output == nil && invocation.automation.failure == nil {
		code := "operation_failed"
		message := "Command failed; see stderr for diagnostics."
		if status == 0 {
			code = "result_unavailable"
			message = "Command completed without a machine-readable result."
			status = 1
			_, _ = fmt.Fprintln(invocation.Stderr, message)
		}
		recordAutomationFailure(invocation, code, message, "")
	}

	output := invocation.automation.output
	if output == nil {
		if invocation.automation.trust {
			output = trustAutomationFailure{Version: automationSchemaVersion, Error: *invocation.automation.failure}
		} else {
			output = automationFailure{SchemaVersion: automationSchemaVersion, Error: *invocation.automation.failure}
		}
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Encode JSON result: %v\n", err)
		return 1
	}
	data = append(data, '\n')
	if _, err := stdout.Write(data); err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Write JSON result: %v\n", err)
		return 1
	}
	return status
}

func singleJSONDocument(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF && strings.TrimSpace(string(data)) != ""
}
