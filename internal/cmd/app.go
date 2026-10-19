package cmd

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/systemd"
)

type App struct {
	Home    string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Client  func(clientID string, key *rsa.PrivateKey) *ghapp.Client
	Exec    func(env []string, name string, args ...string) ([]byte, error)
	Systemd systemd.Runner
	Exe     func() (string, error)
}

// ExecCommand runs the command with env appended to os.Environ() and returns the combined stdout and stderr output.
func ExecCommand(env []string, name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Env = append(os.Environ(), env...)
	return c.CombinedOutput()
}

func (a *App) runTool(env []string, name string, args ...string) error {
	out, err := a.Exec(env, name, args...)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s not found; install it and add it to PATH: %w", name, err)
	}
	cmdline := strings.Join(append([]string{name}, args...), " ")
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s failed: %w\n%s", cmdline, err, msg)
	}
	return fmt.Errorf("%s failed: %w", cmdline, err)
}
