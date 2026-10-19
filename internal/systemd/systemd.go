package systemd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	serviceName = "botid-issue.service"
	timerName   = "botid-issue.timer"

	systemRunDir = "/run/systemd/system"
)

const timerUnit = `[Unit]
Description=Reissue botid credentials every 45 minutes

[Timer]
OnStartupSec=0
OnUnitActiveSec=45min

[Install]
WantedBy=timers.target
`

var errNoSystemctl = errors.New("systemctl not found; systemd is required")

type Runner func(name string, args ...string) ([]byte, error)

// ExecRunner returns the combined output of stdout and stderr.
func ExecRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func Install(home, exe string, run Runner) error {
	return install(home, exe, run, systemRunDir, os.TempDir())
}

// runDir and tempDir are arguments that let tests replace the systemd boot check and the temporary directory check.
func install(home, exe string, run Runner, runDir, tempDir string) error {
	if err := checkExe(exe, tempDir); err != nil {
		return err
	}

	_, err := os.Stat(runDir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("system was not booted with systemd (%s does not exist); no units were created", runDir)
	}
	if err != nil {
		return err
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(unitDir, serviceName), []byte(serviceUnit(exe)), 0o644); err != nil {
		return err
	}

	timerPath := filepath.Join(unitDir, timerName)
	_, err = os.Lstat(timerPath)
	if errors.Is(err, fs.ErrNotExist) {
		err = writeFileAtomic(timerPath, []byte(timerUnit), 0o644)
	}
	if err != nil {
		return err
	}

	if err := systemctl(run, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(run, "enable", "--now", timerName); err != nil {
		return fmt.Errorf("%w\nthe units have been created; to enable them manually, run:\n  systemctl --user daemon-reload\n  systemctl --user enable --now %s", err, timerName)
	}
	return nil
}

func serviceUnit(exe string) string {
	return "[Unit]\n" +
		"Description=Reissue botid credentials\n" +
		"\n" +
		"[Service]\n" +
		"Type=oneshot\n" +
		"ExecStart=\"" + strings.ReplaceAll(exe, "%", "%%") + "\" issue\n" +
		"TimeoutStartSec=2min\n"
}

func checkExe(exe, tempDir string) error {
	switch {
	case !filepath.IsAbs(exe):
		return fmt.Errorf("executable path is not absolute: %q", exe)
	case strings.ContainsAny(exe, "\"\\$\n\r"):
		return fmt.Errorf("executable path contains an unusable character (\", \\, $, newline): %q", exe)
	case strings.Contains(exe, "go-build") || underDir(tempDir, exe):
		return fmt.Errorf("cannot create units from an executable in a temporary directory (a go run binary); run a binary built with go build: %q", exe)
	}
	return nil
}

func underDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func systemctl(run Runner, args ...string) error {
	full := append([]string{"--user"}, args...)
	out, err := run("systemctl", full...)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return errNoSystemctl
	}
	detail := ""
	if msg := strings.TrimRight(string(out), "\r\n"); msg != "" {
		detail = "\n" + msg
	}
	return fmt.Errorf("systemctl %s failed: %w%s", strings.Join(full, " "), err, detail)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = writeAndClose(f, data, perm)
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func writeAndClose(f *os.File, data []byte, perm os.FileMode) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
