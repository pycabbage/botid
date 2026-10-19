package cmd_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
)

func TestExecCommandReturnsCombinedOutputAndAppendsEnv(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	out, err := cmd.ExecCommand([]string{"BOTID_TEST_VALUE=from-env"}, sh, "-c", `echo "out:$BOTID_TEST_VALUE"; echo err >&2`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "out:from-env\nerr\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestExecCommandKeepsParentEnvironment(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	t.Setenv("BOTID_TEST_PARENT", "inherited")
	out, err := cmd.ExecCommand(nil, sh, "-c", `echo "$BOTID_TEST_PARENT"`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "inherited\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestExecCommandNotFoundIsErrNotFound(t *testing.T) {
	_, err := cmd.ExecCommand(nil, "botid-no-such-command-for-test")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error = %v, want exec.ErrNotFound", err)
	}
}

func TestExecCommandFailureReturnsOutputAndError(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	out, err := cmd.ExecCommand(nil, sh, "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("error = %v, want exit status 3", err)
	}
	if got, want := string(out), "boom\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
