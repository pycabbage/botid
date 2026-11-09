package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
)

// gitSettings names settings that botid init writes to the git config file of an identity.
type gitSettings struct {
	UserName, UserEmail, CredentialHelper bool
}

var allGitSettings = gitSettings{UserName: true, UserEmail: true, CredentialHelper: true}

func (s gitSettings) not() gitSettings {
	return gitSettings{!s.UserName, !s.UserEmail, !s.CredentialHelper}
}

func (s gitSettings) empty() bool {
	return s == gitSettings{}
}

func (s gitSettings) names() []string {
	var names []string
	if s.UserName {
		names = append(names, "user.name")
	}
	if s.UserEmail {
		names = append(names, "user.email")
	}
	if s.CredentialHelper {
		names = append(names, "the gh credential helper")
	}
	return names
}

// readGitSettings reports which settings the git config file of the identity has. A file that does not exist has none.
// git parses the file, so quoting and case rules are git's own. With --file, git does not follow include directives.
func (a *App) readGitSettings(ctx context.Context, id paths.Identity) (gitSettings, error) {
	if _, err := os.Stat(id.GitConfigFile); errors.Is(err, fs.ErrNotExist) {
		return gitSettings{}, nil
	} else if err != nil {
		return gitSettings{}, fmt.Errorf("cannot read %s: %w", id.GitConfigFile, err)
	}
	out, err := a.Git(ctx, id.GitConfigDir, "config", "--file", id.GitConfigFile, "--list", "-z")
	if err != nil {
		return gitSettings{}, fmt.Errorf("cannot read %s: %w", id.GitConfigFile, err)
	}
	var present gitSettings
	for entry := range strings.SplitSeq(string(out), "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		switch key {
		case "user.name":
			present.UserName = value != ""
		case "user.email":
			present.UserEmail = value != ""
		case "credential.https://github.com.helper":
			present.CredentialHelper = present.CredentialHelper || strings.Contains(value, "auth git-credential")
		}
	}
	return present, nil
}

// identityReady reports whether the identity has what botid init leaves: a hosts.yml and a complete git config file.
// It reads local files only and never looks at the token, which botid issue renews.
func (a *App) identityReady(ctx context.Context, id paths.Identity) (bool, error) {
	if _, err := os.Stat(id.HostsFile); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("cannot read %s: %w", id.HostsFile, err)
	}
	present, err := a.readGitSettings(ctx, id)
	return present == allGitSettings, err
}

// applyGit writes the given settings to the git config file of the identity. The file is the one in GIT_CONFIG_GLOBAL,
// so the user's own git and gh configuration is not touched. gh auth setup-git reads the hosts.yml of the identity.
func (a *App) applyGit(ctx context.Context, id paths.Identity, client *ghapp.Client, cred ghapp.Credential, which gitSettings) error {
	if err := ensureDirs(id.GitConfigDir); err != nil {
		return err
	}
	botName := cred.Slug + "[bot]"
	var email string
	if which.UserEmail {
		userID, err := client.BotUserID(ctx, cred.Token, cred.Slug)
		if err != nil {
			return err
		}
		email = fmt.Sprintf("%d+%s@users.noreply.github.com", userID, botName)
	}
	env := []string{"GH_CONFIG_DIR=" + id.GHConfigDir, "GIT_CONFIG_GLOBAL=" + id.GitConfigFile}
	if which.UserName {
		if err := a.runTool(ctx, env, "git", "config", "--global", "user.name", botName); err != nil {
			return err
		}
	}
	if which.UserEmail {
		if err := a.runTool(ctx, env, "git", "config", "--global", "user.email", email); err != nil {
			return err
		}
	}
	if which.CredentialHelper {
		return a.runTool(ctx, env, "gh", "auth", "setup-git")
	}
	return nil
}

// withLock runs fn while it holds an exclusive lock on the configuration directory, so that botid processes
// that run together (the timer's issue, the env of a shell command, init) do not write identity files at the same time.
// The directory exists whenever config.json does, and the lock creates nothing. fn must not take the lock again.
// issue requests its credential before it takes the lock. ensureIdentity requests inside it on purpose: commands that
// start together then wait for the first one and find the identity complete, instead of each issuing a token.
func withLock(p paths.Paths, fn func() error) error {
	dir, err := os.Open(p.ConfigDir)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", p.ConfigDir, err)
	}
	defer func() { _ = dir.Close() }()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("cannot lock %s: %w", p.ConfigDir, err)
	}
	return fn()
}

// issueCredential issues an installation token for the App identity.
func (a *App) issueCredential(ctx context.Context, ident config.Identity) (*ghapp.Client, ghapp.Credential, error) {
	if ident.InstallationID == 0 {
		return nil, ghapp.Credential{}, errors.New("has no installationId; run botid init again")
	}
	key, err := ident.Key()
	if err != nil {
		return nil, ghapp.Credential{}, err
	}
	client := a.Client(ident.ClientID, key)
	cred, err := client.Issue(ctx, ident.InstallationID)
	return client, cred, err
}

// completeIdentity writes a new hosts.yml for the App identity, then adds the git settings that its git config file lacks.
// It adds only what is missing, so a value that the user changed stays, and a complete file causes no further request.
// It returns the names of the settings that it added. The caller holds the lock.
func (a *App) completeIdentity(ctx context.Context, p paths.Paths, id paths.Identity, client *ghapp.Client, cred ghapp.Credential) ([]string, error) {
	if err := writeHosts(p, id, cred); err != nil {
		return nil, err
	}
	present, err := a.readGitSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	missing := present.not()
	if missing.empty() {
		return nil, nil
	}
	return missing.names(), a.applyGit(ctx, id, client, cred, missing)
}

// ensureIdentity completes the selected App identity when it is not complete, as botid init would, from the settings in config.json.
// A complete identity costs local reads and no request. An incomplete one needs the network, and a failure stops the command.
func (a *App) ensureIdentity(ctx context.Context, p paths.Paths, c config.Config, name string) error {
	id := p.Identity(name)
	ready, err := a.identityReady(ctx, id)
	if err != nil {
		return fmt.Errorf("identity %q: %w", name, err)
	}
	if ready {
		return nil
	}

	var added []string
	repaired := false
	err = withLock(p, func() error {
		ready, err := a.identityReady(ctx, id)
		if err != nil || ready {
			return err
		}
		repaired = true
		client, cred, err := a.issueCredential(ctx, c.Identities[name])
		if err != nil {
			return err
		}
		added, err = a.completeIdentity(ctx, p, id, client, cred)
		return err
	})
	if err != nil {
		return fmt.Errorf("identity %q: %w", name, err)
	}
	switch {
	case !repaired:
	case len(added) == 0:
		a.notef("identity %q was incomplete; wrote %s", name, id.HostsFile)
	default:
		a.notef("identity %q was incomplete; wrote %s and added %s to %s", name, id.HostsFile, strings.Join(added, ", "), id.GitConfigFile)
	}
	return nil
}
