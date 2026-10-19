package cmd_test

import (
	"strings"
	"testing"
)

func TestEnvPrintsOnlyDotEnvToStdout(t *testing.T) {
	h := newHarness(t, "")

	if err := h.app.Env(); err != nil {
		t.Fatalf("Env: %v", err)
	}

	want := "GH_CONFIG_DIR='" + h.home + "/.local/share/botid/gh'\n" +
		"GIT_CONFIG_GLOBAL='" + h.home + "/.local/share/botid/git/config'\n"
	if got := h.stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestEnvGuidanceGoesToStderr(t *testing.T) {
	h := newHarness(t, "")

	if err := h.app.Env(); err != nil {
		t.Fatalf("Env: %v", err)
	}

	want := "Set the environment variables above once in the coding agent's environment. Their values are fixed and never change.\n"
	if got := h.stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if strings.Contains(h.stdout.String(), "coding agent") {
		t.Errorf("guidance leaked into stdout: %q", h.stdout.String())
	}
}

func TestEnvWorksWithoutConfigAndTouchesNothing(t *testing.T) {
	h := newHarness(t, "")

	if err := h.app.Env(); err != nil {
		t.Fatalf("Env: %v", err)
	}
	h.assertHomeUntouched()
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.rec.lines(); len(got) != 0 {
		t.Errorf("external commands were run: %v", got)
	}
}

func TestEnvRejectsHomeWithSingleQuote(t *testing.T) {
	h := newHarness(t, "")
	h.app.Home = "/home/o'brien"

	err := h.app.Env()
	if err == nil || !strings.Contains(err.Error(), "must not contain a single quote or newline") {
		t.Fatalf("error = %v, want an error about a path containing a single quote", err)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("output was written to stdout on error: %q", h.stdout.String())
	}
}
