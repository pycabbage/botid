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

	c, err := config.Load(p.ConfigFile)
	fresh := false
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fresh = true
	case errors.Is(err, config.ErrLegacy):
		c, err = a.migrate(ctx, p)
		if err != nil {
			return err
		}
	case err != nil:
		return err
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

	name := cred.Slug
	if err := config.ValidName(name); err != nil {
		return fmt.Errorf("cannot use the App slug as an identity name: %w", err)
	}
	if _, exists := c.Identities[name]; exists {
		proceed, err := a.confirmOverwrite(in, fmt.Sprintf("Warning: identity %q already exists in %s. If you continue, its credentials and private key will be overwritten.\n", name, p.ConfigFile))
		if err != nil {
			return err
		}
		if !proceed {
			a.printExited()
			return nil
		}
	}

	id := p.Identity(name)
	if err := writeIdentity(p, id, pemBytes, hosts); err != nil {
		return err
	}
	botName := name + "[bot]"
	if err := a.setupGit(ctx, id, client, cred, botName); err != nil {
		return err
	}
	isDefault := fresh || c.Default == name
	if fresh {
		c = config.Config{Default: name}
	}
	if c.Identities == nil {
		c.Identities = map[string]config.Identity{}
	}
	c.Identities[name] = config.Identity{
		ClientID:       clientID,
		PrivateKeyPath: id.PrivateKeyFile,
		InstallationID: installation.ID,
	}
	if err := config.Save(p.ConfigFile, c); err != nil {
		return fmt.Errorf("cannot write %s: %w", p.ConfigFile, err)
	}

	if err := systemd.Install(ctx, a.Home, exe, a.Systemd); err != nil {
		return fmt.Errorf("the configuration and credentials were saved, but registering the systemd timer failed: %w", err)
	}

	role := "not the default"
	if isDefault {
		role = "default"
	}
	_, _ = fmt.Fprintf(a.Stdout, `
botid init is complete.
  Identity:     %s (%s)
  Config:       %s
  Private key:  %s
  Credentials:  %s (%s)
  git config:   %s (user.name, user.email, credential helper)
  systemd:      enabled botid-issue.timer (runs botid issue every 45 minutes)

Next steps:
  To use an identity for specific repositories or branches, add rules to "rules" in %s.

Note:
  To keep the timer running while you are logged out, run loginctl enable-linger. botid does not change this setting.
`, name, role, p.ConfigFile, id.PrivateKeyFile, id.HostsFile, botName, id.GitConfigFile, p.ConfigFile)
	return nil
}

func (a *App) printExited() {
	_, _ = fmt.Fprintln(a.Stdout, "Exited without changing anything.")
}

func (a *App) confirmOverwrite(in *bufio.Reader, warning string) (bool, error) {
	_, _ = fmt.Fprint(a.Stderr, warning)
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

// writeIdentity writes the private key and hosts.yml of one identity. config.json is written later, once the whole setup has worked.
func writeIdentity(p paths.Paths, id paths.Identity, pemBytes, hosts []byte) error {
	if err := ensureDirs(p.ConfigDir, filepath.Dir(id.ConfigDir), id.ConfigDir); err != nil {
		return err
	}
	if err := store.WriteFileAtomic(id.PrivateKeyFile, pemBytes, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", id.PrivateKeyFile, err)
	}
	if err := ensureDirs(p.DataDir, filepath.Dir(id.DataDir), id.DataDir, id.GHConfigDir); err != nil {
		return err
	}
	if err := store.WriteFileAtomic(id.HostsFile, hosts, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", id.HostsFile, err)
	}
	return nil
}

func (a *App) setupGit(ctx context.Context, id paths.Identity, client *ghapp.Client, cred ghapp.Credential, botName string) error {
	if err := ensureDirs(id.GitConfigDir); err != nil {
		return err
	}
	userID, err := client.BotUserID(ctx, cred.Token, cred.Slug)
	if err != nil {
		return err
	}
	env := []string{"GH_CONFIG_DIR=" + id.GHConfigDir, "GIT_CONFIG_GLOBAL=" + id.GitConfigFile}
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
