package plugin

import (
	"os"
	"testing"
)

func TestScrubbedEnv(t *testing.T) {
	// Set a test env var to verify scrubbing.
	t.Setenv("SECRET_TOKEN", "should-be-scrubbed")
	t.Setenv("ALLOWED_VAR", "should-be-kept")

	env := scrubbedEnv([]string{"ALLOWED_VAR"})

	envMap := make(map[string]bool)
	for _, e := range env {
		for i, ch := range e {
			if ch == '=' {
				envMap[e[:i]] = true
				break
			}
		}
	}

	// PATH and HOME should always be present (unless somehow unset).
	if path := os.Getenv("PATH"); path != "" && !envMap["PATH"] {
		t.Error("PATH should be in scrubbed env")
	}
	if home := os.Getenv("HOME"); home != "" && !envMap["HOME"] {
		t.Error("HOME should be in scrubbed env")
	}

	// SECRET_TOKEN must be scrubbed.
	if envMap["SECRET_TOKEN"] {
		t.Error("SECRET_TOKEN should have been scrubbed")
	}

	// ALLOWED_VAR should be kept.
	if !envMap["ALLOWED_VAR"] {
		t.Error("ALLOWED_VAR should be in scrubbed env")
	}
}

func TestScrubbedEnv_EmptyAllowlist(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "secret")
	t.Setenv("AWS_SECRET_KEY", "secret")

	env := scrubbedEnv(nil)

	for _, e := range env {
		for i, ch := range e {
			if ch == '=' {
				key := e[:i]
				if key == "JIRA_TOKEN" || key == "AWS_SECRET_KEY" {
					t.Errorf("%s should have been scrubbed", key)
				}
				break
			}
		}
	}
}
