package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/store"
	"github.com/pycabbage/botid/internal/systemd"
)

func (a *App) Init(ctx context.Context) error {
	p := paths.New(a.Home)
	in := bufio.NewReader(a.Stdin)

	exe, err := a.Exe()
	if err != nil {
		return fmt.Errorf("cannot get the path of the botid executable: %w", err)
	}
	if err := systemd.Preflight(exe); err != nil {
		return err
	}

	proceed, err := a.confirmOverwrite(in, p.ConfigFile)
	if err != nil {
		return err
	}
	if !proceed {
		_, _ = fmt.Fprintln(a.Stdout, "Exited without changing anything.")
		return nil
	}

	_, _ = fmt.Fprint(a.Stdout, "Client ID: ")
	clientID, err := readLine(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("cannot read standard input: %w", err)
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return errors.New("Client ID is empty") //nolint:staticcheck // ST1005: "Client ID" is the GitHub App field name and the prompt label; the exact text is asserted by main_test.go and init_test.go
	}

	_, _ = fmt.Fprint(a.Stdout, "Private Key (PEM contents or file path): ")
	pemBytes, err := a.readPrivateKey(in)
	if err != nil {
		return err
	}
	key, err := ghapp.ParsePEM(pemBytes)
	if err != nil {
		return err
	}

	client := a.Client(clientID, key)
	installation, err := a.chooseInstallation(ctx, in, client)
	if err != nil {
		return err
	}
	cred, err := client.Issue(ctx, installation.ID)
	if err != nil {
		return err
	}
	hosts, err := store.HostsYAML(cred.Slug, cred.Token)
	if err != nil {
		return fmt.Errorf("cannot generate hosts.yml: %w", err)
	}

	if err := writeCredentials(p, config.Config{
		ClientID:       clientID,
		PrivateKeyPath: p.PrivateKeyFile,
		InstallationID: installation.ID,
	}, pemBytes, hosts); err != nil {
		return err
	}

	botName := cred.Slug + "[bot]"
	if err := a.setupGit(ctx, p, client, cred, botName); err != nil {
		return err
	}

	if err := systemd.Install(ctx, a.Home, exe, a.Systemd); err != nil {
		return fmt.Errorf("the configuration and credentials were saved, but registering the systemd timer failed: %w", err)
	}

	_, _ = fmt.Fprintf(a.Stdout, `
botid init is complete.
  Config:       %s
                %s
  Credentials:  %s (%s)
  git config:   %s (user.name, user.email, credential helper)
  systemd:      enabled botid-issue.timer (runs botid issue every 45 minutes)

Next steps:
  Run botid env and set the printed environment variables once in the coding agent's environment.

Note:
  To keep the timer running while you are logged out, run loginctl enable-linger. botid does not change this setting.
`, p.ConfigFile, p.PrivateKeyFile, p.HostsFile, botName, p.GitConfigFile)
	return nil
}

func (a *App) confirmOverwrite(in *bufio.Reader, configFile string) (bool, error) {
	_, err := os.Stat(configFile)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot check %s: %w", configFile, err)
	}
	_, _ = fmt.Fprintf(a.Stderr, "Warning: %s already exists. If you continue, the configuration and private key will be overwritten.\n", configFile)
	_, _ = fmt.Fprint(a.Stdout, "Overwrite? [y/n]: ")
	answer, err := readLine(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("cannot read standard input: %w", err)
	}
	return strings.TrimSpace(answer) == "y", nil
}

func (a *App) readPrivateKey(in *bufio.Reader) ([]byte, error) {
	first, err := readLine(in)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("cannot read standard input: %w", err)
	}
	first = strings.TrimSpace(first)

	if strings.HasPrefix(first, "-----BEGIN") {
		var b strings.Builder
		for line := first; ; {
			b.WriteString(line + "\n")
			if strings.HasPrefix(line, "-----END") {
				return []byte(b.String()), nil
			}
			next, err := readLine(in)
			if err != nil {
				return nil, errors.New("input ended before the PEM -----END line was found")
			}
			line = strings.TrimSpace(next)
		}
	}

	if first == "" {
		return nil, errors.New("private key is empty; enter the PEM contents or a file path")
	}
	path := first
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		path = filepath.Join(a.Home, rest)
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read private key: %w", err)
	}
	return pemBytes, nil
}

func (a *App) chooseInstallation(ctx context.Context, in *bufio.Reader, client *ghapp.Client) (ghapp.Installation, error) {
	installations, err := client.Installations(ctx)
	if err != nil {
		return ghapp.Installation{}, err
	}
	switch len(installations) {
	case 0:
		return ghapp.Installation{}, errors.New("this App is not installed on any account")
	case 1:
		_, _ = fmt.Fprintf(a.Stdout, "Installation: %s (id: %d)\n", installations[0].Login, installations[0].ID)
		return installations[0], nil
	}

	_, _ = fmt.Fprintln(a.Stdout, "This App is installed on multiple accounts:")
	for i, it := range installations {
		_, _ = fmt.Fprintf(a.Stdout, "  %d) %s (id: %d)\n", i+1, it.Login, it.ID)
	}
	_, _ = fmt.Fprintf(a.Stdout, "Installation [1-%d]: ", len(installations))
	answer, err := readLine(in)
	if err != nil {
		return ghapp.Installation{}, fmt.Errorf("cannot read the installation number: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil {
		return ghapp.Installation{}, fmt.Errorf("installation number %q is not a number", strings.TrimSpace(answer))
	}
	if n < 1 || n > len(installations) {
		return ghapp.Installation{}, fmt.Errorf("installation number %d is out of range (1-%d)", n, len(installations))
	}
	return installations[n-1], nil
}

func writeCredentials(p paths.Paths, c config.Config, pemBytes, hosts []byte) error {
	if err := store.EnsureDir(p.ConfigDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", p.ConfigDir, err)
	}
	if err := store.WriteFileAtomic(p.PrivateKeyFile, pemBytes, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", p.PrivateKeyFile, err)
	}
	if err := config.Save(p.ConfigFile, c); err != nil {
		return fmt.Errorf("cannot write %s: %w", p.ConfigFile, err)
	}
	if err := store.EnsureDir(p.DataDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", p.DataDir, err)
	}
	if err := store.EnsureDir(p.GHConfigDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", p.GHConfigDir, err)
	}
	if err := store.WriteFileAtomic(p.HostsFile, hosts, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", p.HostsFile, err)
	}
	return nil
}

func (a *App) setupGit(ctx context.Context, p paths.Paths, client *ghapp.Client, cred ghapp.Credential, botName string) error {
	if err := store.EnsureDir(p.GitConfigDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", p.GitConfigDir, err)
	}
	userID, err := client.BotUserID(ctx, cred.Token, cred.Slug)
	if err != nil {
		return err
	}
	env := []string{"GH_CONFIG_DIR=" + p.GHConfigDir, "GIT_CONFIG_GLOBAL=" + p.GitConfigFile}
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", userID, botName)
	if err := a.runTool(ctx, env, "git", "config", "--global", "user.name", botName); err != nil {
		return err
	}
	if err := a.runTool(ctx, env, "git", "config", "--global", "user.email", email); err != nil {
		return err
	}
	return a.runTool(ctx, env, "gh", "auth", "setup-git")
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
