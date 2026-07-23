package application

import (
	"fmt"
	"io"
	"os"
)

const (
	cloneTimeoutEnvironment           = "OPEN_SKILLS_CLONE_TIMEOUT_MS"
	legacyCloneTimeoutEnvironment     = "SKILLS_CLONE_TIMEOUT_MS"
	installInternalEnvironment        = "OPEN_SKILLS_INSTALL_INTERNAL_SKILLS"
	legacyInstallInternalEnvironment  = "INSTALL_INTERNAL_SKILLS"
	suppressLegacyWarningsEnvironment = "OPEN_SKILLS_SUPPRESS_LEGACY_ENV_WARNINGS"
)

type legacyEnvironmentVariable struct {
	legacy    string
	canonical string
}

var legacyEnvironmentVariables = []legacyEnvironmentVariable{
	{legacy: legacyCloneTimeoutEnvironment, canonical: cloneTimeoutEnvironment},
	{legacy: legacyInstallInternalEnvironment, canonical: installInternalEnvironment},
}

func reportLegacyEnvironmentWarnings(stderr io.Writer) {
	if stderr == nil || environmentBoolean(suppressLegacyWarningsEnvironment, "") {
		return
	}
	for _, variable := range legacyEnvironmentVariables {
		if _, exists := os.LookupEnv(variable.legacy); !exists {
			continue
		}
		if _, canonicalExists := os.LookupEnv(variable.canonical); canonicalExists {
			_, _ = fmt.Fprintf(stderr, "Warning: %s is deprecated and ignored because %s is set; remove %s before open-skills 2.0.\n", variable.legacy, variable.canonical, variable.legacy)
			continue
		}
		_, _ = fmt.Fprintf(stderr, "Warning: %s is deprecated and will be removed in open-skills 2.0; use %s instead.\n", variable.legacy, variable.canonical)
	}
}

func environmentValue(canonical, legacy string) string {
	if value, exists := os.LookupEnv(canonical); exists {
		return value
	}
	if legacy != "" {
		return os.Getenv(legacy)
	}
	return ""
}

func environmentBoolean(canonical, legacy string) bool {
	value := environmentValue(canonical, legacy)
	return value == "1" || value == "true"
}
