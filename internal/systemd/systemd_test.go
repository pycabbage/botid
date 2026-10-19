package systemd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

type recorder struct {
	calls []call
	errs  map[string]error
	out   map[string]string
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name, args})
	key := strings.Join(args, " ")
	return []byte(r.out[key]), r.errs[key]
}

type env struct {
	home    string
	runDir  string
	tempDir string
	exe     string
	unitDir string
}

func newEnv(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	runDir := filepath.Join(root, "run-systemd-system")
	if err := os.Mkdir(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return env{
		home:    home,
		runDir:  runDir,
		tempDir: filepath.Join(root, "not-the-real-tmp"),
		exe:     filepath.Join(root, "bin", "botid"),
		unitDir: filepath.Join(home, ".config", "systemd", "user"),
	}
}

func (e env) install(run Runner) error {
	return install(e.home, e.exe, run, e.runDir, e.tempDir)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallWritesUnitsAndRunsSystemctlInOrder(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}

	if err := e.install(rec.run); err != nil {
		t.Fatal(err)
	}

	wantCalls := []call{
		{"systemctl", []string{"--user", "daemon-reload"}},
		{"systemctl", []string{"--user", "enable", "--now", "botid-issue.timer"}},
	}
	if !reflect.DeepEqual(rec.calls, wantCalls) {
		t.Errorf("calls = %v, want %v", rec.calls, wantCalls)
	}

	wantService := "[Unit]\n" +
		"Description=Reissue botid credentials\n" +
		"\n" +
		"[Service]\n" +
		"Type=oneshot\n" +
		"ExecStart=\"" + e.exe + "\" issue\n" +
		"TimeoutStartSec=2min\n"
	if got := readFile(t, filepath.Join(e.unitDir, "botid-issue.service")); got != wantService {
		t.Errorf("service =\n%s\nwant\n%s", got, wantService)
	}

	wantTimer := "[Unit]\n" +
		"Description=Reissue botid credentials every 45 minutes\n" +
		"\n" +
		"[Timer]\n" +
		"OnStartupSec=0\n" +
		"OnUnitActiveSec=45min\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=timers.target\n"
	if got := readFile(t, filepath.Join(e.unitDir, "botid-issue.timer")); got != wantTimer {
		t.Errorf("timer =\n%s\nwant\n%s", got, wantTimer)
	}
}

func TestInstallCreatesUnitDirWith0755AndLeavesExistingDirMode(t *testing.T) {
	e := newEnv(t)
	if err := e.install((&recorder{}).run); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(e.unitDir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("created dir mode = %o, want 755", fi.Mode().Perm())
	}

	e2 := newEnv(t)
	if err := os.MkdirAll(e2.unitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e2.unitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := e2.install((&recorder{}).run); err != nil {
		t.Fatal(err)
	}
	fi, err = os.Stat(e2.unitDir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("existing dir mode = %o, want 700", fi.Mode().Perm())
	}
}

func TestInstallLeavesNoTempFiles(t *testing.T) {
	e := newEnv(t)
	if err := e.install((&recorder{}).run); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(e.unitDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ent := range entries {
		names = append(names, ent.Name())
	}
	want := []string{"botid-issue.service", "botid-issue.timer"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("files = %v, want %v", names, want)
	}
}

func TestInstallEscapesPercent(t *testing.T) {
	e := newEnv(t)
	e.exe = filepath.Join(filepath.Dir(e.exe), "100%", "botid")

	if err := e.install((&recorder{}).run); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(e.unitDir, "botid-issue.service"))
	want := "ExecStart=\"" + strings.ReplaceAll(e.exe, "100%", "100%%") + "\" issue\n"
	if !strings.Contains(got, want) || !strings.Contains(got, "/100%%/botid\" issue\n") {
		t.Errorf("service = %q, want it to contain %q with %%%% escape", got, want)
	}
}

func TestInstallRejectsUnsafeExePaths(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{
		"relative path":       "botid",
		"relative path (./)":  "./botid",
		"empty":               "",
		"double quote":        "/opt/bo\"tid/botid",
		"backslash":           "/opt/bo\\tid/botid",
		"dollar sign":         "/opt/bo$tid/botid",
		"newline":             "/opt/bo\ntid/botid",
		"carriage return":     "/opt/bo\rtid/botid",
		"go-build":            "/home/u/.cache/go-build/ab/botid",
		"go-build standalone": "/var/go-build12/exe/botid",
	}
	for name, exe := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			err := install(e.home, exe, rec.run, e.runDir, e.tempDir)
			if err == nil {
				t.Fatalf("install(%q) succeeded, want error", exe)
			}
			if len(rec.calls) != 0 {
				t.Errorf("systemctl was called: %v", rec.calls)
			}
			if _, statErr := os.Stat(e.unitDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("unit dir was created (stat err = %v)", statErr)
			}
		})
	}
}

func TestInstallRejectsExeUnderTempDir(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}

	cases := []string{
		filepath.Join(e.tempDir, "botid"),
		filepath.Join(e.tempDir, "a", "b", "botid"),
	}
	for _, exe := range cases {
		err := install(e.home, exe, rec.run, e.runDir, e.tempDir)
		if err == nil {
			t.Fatalf("install(%q) succeeded, want error", exe)
		}
		if !strings.Contains(err.Error(), "temporary directory") {
			t.Errorf("err = %q, want temporary directory", err)
		}
	}
	if len(rec.calls) != 0 {
		t.Errorf("systemctl was called: %v", rec.calls)
	}
	if _, err := os.Stat(e.unitDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unit dir was created (stat err = %v)", err)
	}
}

func TestInstallAcceptsSiblingOfTempDirPrefix(t *testing.T) {
	e := newEnv(t)
	e.exe = e.tempDir + "-other/botid"
	if err := e.install((&recorder{}).run); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsExeUnderRealOSTempDir(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(os.TempDir(), "botid-test-nonexistent", "botid")
	rec := &recorder{}

	err := Install(home, exe, rec.run)
	if err == nil {
		t.Fatal("Install succeeded, want error")
	}
	if len(rec.calls) != 0 {
		t.Errorf("systemctl was called: %v", rec.calls)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".config")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("home was modified (stat err = %v)", statErr)
	}
}

func TestInstallFailsWithoutWritingWhenSystemdIsNotRunning(t *testing.T) {
	e := newEnv(t)
	e.runDir = filepath.Join(e.runDir, "missing")
	rec := &recorder{}

	err := e.install(rec.run)
	if err == nil {
		t.Fatal("install succeeded, want error")
	}
	if !strings.Contains(err.Error(), "system was not booted with systemd") {
		t.Errorf("err = %q", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("systemctl was called: %v", rec.calls)
	}
	if _, statErr := os.Stat(filepath.Join(e.home, ".config")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("home was modified (stat err = %v)", statErr)
	}
}

func TestInstallRewritesServiceButKeepsExistingTimer(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(e.unitDir, "botid-issue.service")
	timerPath := filepath.Join(e.unitDir, "botid-issue.timer")
	edited := "[Timer]\nOnUnitActiveSec=10min\n"
	if err := os.WriteFile(timerPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servicePath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	if err := e.install(rec.run); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, timerPath); got != edited {
		t.Errorf("timer was modified: %q", got)
	}
	fi, err := os.Stat(timerPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("timer mode = %o, want 600", fi.Mode().Perm())
	}
	if got := readFile(t, servicePath); !strings.Contains(got, "ExecStart=\""+e.exe+"\" issue\n") {
		t.Errorf("service was not rewritten: %q", got)
	}
	if len(rec.calls) != 2 {
		t.Errorf("calls = %v, want daemon-reload and enable", rec.calls)
	}

	e.exe = filepath.Join(filepath.Dir(e.exe), "moved", "botid")
	if err := e.install(rec.run); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, servicePath); !strings.Contains(got, "ExecStart=\""+e.exe+"\" issue\n") {
		t.Errorf("service did not follow the moved binary: %q", got)
	}
	if got := readFile(t, timerPath); got != edited {
		t.Errorf("timer was modified on second run: %q", got)
	}
}

func TestInstallSystemctlNotFound(t *testing.T) {
	e := newEnv(t)
	run := func(name string, args ...string) ([]byte, error) {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}

	err := e.install(run)
	if err == nil {
		t.Fatal("install succeeded, want error")
	}
	if err.Error() != "systemctl not found; systemd is required" {
		t.Errorf("err = %q", err)
	}
}

func TestInstallDaemonReloadFailureIncludesOutput(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{
		errs: map[string]error{"--user daemon-reload": errors.New("exit status 1")},
		out:  map[string]string{"--user daemon-reload": "Failed to connect to bus: No medium found\n"},
	}

	err := e.install(rec.run)
	if err == nil {
		t.Fatal("install succeeded, want error")
	}
	if !strings.Contains(err.Error(), "Failed to connect to bus: No medium found") {
		t.Errorf("err = %q, want systemctl output", err)
	}
	if len(rec.calls) != 1 {
		t.Errorf("calls = %v, want only daemon-reload", rec.calls)
	}
}

func TestInstallEnableFailureShowsManualCommands(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{
		errs: map[string]error{"--user enable --now botid-issue.timer": errors.New("exit status 1")},
		out:  map[string]string{"--user enable --now botid-issue.timer": "Failed to enable unit: Access denied\n"},
	}

	err := e.install(rec.run)
	if err == nil {
		t.Fatal("install succeeded, want error")
	}
	msg := err.Error()
	for _, want := range []string{
		"Failed to enable unit: Access denied",
		"the units have been created; to enable them manually, run:",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now botid-issue.timer",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("err = %q, want it to contain %q", msg, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(e.unitDir, "botid-issue.service")); statErr != nil {
		t.Errorf("service should remain: %v", statErr)
	}
}

func TestGeneratedUnitsPassSystemdAnalyzeVerify(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze not found")
	}

	e := newEnv(t)
	binDir := filepath.Join(filepath.Dir(e.exe), "100%")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e.exe = filepath.Join(binDir, "botid")
	if err := os.WriteFile(e.exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.install((&recorder{}).run); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(analyze, "--user", "verify",
		filepath.Join(e.unitDir, "botid-issue.service"),
		filepath.Join(e.unitDir, "botid-issue.timer"))
	cmd.Env = append(os.Environ(), "HOME="+e.home, "XDG_CONFIG_HOME="+filepath.Join(e.home, ".config"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("systemd-analyze verify: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("systemd-analyze verify output = %q, want empty", out)
	}
}

func TestExecRunnerReturnsCombinedOutput(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	out, err := ExecRunner(sh, "-c", "echo out; echo err >&2")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "out\nerr\n" {
		t.Errorf("out = %q", got)
	}
}

func TestExecRunnerNotFoundIsErrNotFound(t *testing.T) {
	_, err := ExecRunner("botid-no-such-command-for-test")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want exec.ErrNotFound", err)
	}
}

func TestSystemctlOutputIsKeptVerbatimInError(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		return []byte("connection failed\nline2\n"), errors.New("exit status 1")
	}
	err := systemctl(run, "daemon-reload")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "connection failed\nline2") {
		t.Errorf("err = %q", err)
	}
}
