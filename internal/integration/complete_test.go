package integration

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/store"
)

// A GitHub API fake for one App named hand-bot, installed as 1, whose bot user has the ID 4242.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/1/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"token":"ghs_realish","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"slug":"hand-bot"}`) })
	mux.HandleFunc("/users/hand-bot[bot]", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"id":4242}`) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEnvCompletesAHandWrittenConfigWithRealGitAndGh(t *testing.T) {
	guardRealHome(t)
	requireTools(t, "gh", "git")
	isolateGit(t)
	srv := fakeGitHub(t)

	home := t.TempDir()
	p := paths.New(home)
	id := p.Identity("hand-bot")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureDir(p.ConfigDir); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(p.ConfigDir, "hand-bot.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		Default:    "hand-bot",
		Identities: map[string]config.Identity{"hand-bot": {ClientID: "Iv23liA", PrivateKeyPath: keyFile, InstallationID: 1}},
	}
	if err := config.Save(p.ConfigFile, c); err != nil {
		t.Fatal(err)
	}

	newApp := func(stdout, stderr *bytes.Buffer) *cmd.App {
		return &cmd.App{
			Home: home, Cwd: home, Stdout: stdout, Stderr: stderr,
			Client: func(clientID string, k *rsa.PrivateKey) *ghapp.Client {
				return &ghapp.Client{BaseURL: srv.URL, ClientID: clientID, Key: k}
			},
			Exec: cmd.ExecCommand,
			Git:  repo.ExecGit,
		}
	}
	sandbox := sandbox{home: home, id: id}

	t.Run("the first command completes the identity", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := newApp(&stdout, &stderr).Env(t.Context(), home, cmd.FormatJSON); err != nil {
			t.Fatalf("Env: %v\nstderr: %s", err, stderr.String())
		}

		if !strings.Contains(stderr.String(), `identity "hand-bot" was incomplete`) {
			t.Errorf("stderr = %q", stderr.String())
		}
		if got := strings.TrimSpace(sandbox.mustRun(t, "", "git", "config", "--global", "--get", "user.name")); got != "hand-bot[bot]" {
			t.Errorf("user.name = %q", got)
		}
		if got := strings.TrimSpace(sandbox.mustRun(t, "", "git", "config", "--global", "--get", "user.email")); got != "4242+hand-bot[bot]@users.noreply.github.com" {
			t.Errorf("user.email = %q", got)
		}
		if got := strings.TrimSpace(sandbox.mustRun(t, "", "gh", "auth", "token")); got != "ghs_realish" {
			t.Errorf("gh auth token = %q", got)
		}
		out := sandbox.mustRun(t, "protocol=https\nhost=github.com\n\n", "git", "credential", "fill")
		for _, want := range []string{"username=hand-bot[bot]\n", "password=ghs_realish\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("git credential fill output does not contain %q:\n%s", want, out)
			}
		}
	})

	t.Run("a second command finds it complete and changes nothing", func(t *testing.T) {
		before := readFile(t, id.GitConfigFile)
		hostsBefore := snapshot(t, id.HostsFile)
		var stdout, stderr bytes.Buffer

		if err := newApp(&stdout, &stderr).Env(t.Context(), home, cmd.FormatJSON); err != nil {
			t.Fatalf("Env: %v", err)
		}

		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty", stderr.String())
		}
		if got := readFile(t, id.GitConfigFile); got != before {
			t.Errorf("git config changed:\n%s\nwant\n%s", got, before)
		}
		if !hostsBefore.equal(snapshot(t, id.HostsFile)) {
			t.Error("hosts.yml was rewritten")
		}
	})

	t.Run("a value that the user changed stays, and only what is missing is added", func(t *testing.T) {
		sandbox.mustRun(t, "", "git", "config", "--global", "user.name", "My Own Name")
		sandbox.mustRun(t, "", "git", "config", "--global", "--unset-all", "user.email")
		var stdout, stderr bytes.Buffer

		if err := newApp(&stdout, &stderr).Env(t.Context(), home, cmd.FormatJSON); err != nil {
			t.Fatalf("Env: %v", err)
		}

		if got := strings.TrimSpace(sandbox.mustRun(t, "", "git", "config", "--global", "--get", "user.name")); got != "My Own Name" {
			t.Errorf("user.name = %q, want the user's value to stay", got)
		}
		if got := strings.TrimSpace(sandbox.mustRun(t, "", "git", "config", "--global", "--get", "user.email")); got != "4242+hand-bot[bot]@users.noreply.github.com" {
			t.Errorf("user.email = %q", got)
		}
		if !strings.Contains(stderr.String(), "added user.email to") || strings.Contains(stderr.String(), "user.name") {
			t.Errorf("stderr = %q, want a notice that only user.email was added", stderr.String())
		}
	})

	t.Run("a manual gh auth setup-git leaves user.name and user.email missing, and the next command adds them", func(t *testing.T) {
		if err := os.Remove(id.GitConfigFile); err != nil {
			t.Fatal(err)
		}
		sandbox.mustRun(t, "", "gh", "auth", "setup-git")
		if r := sandbox.exec(t, "", "git", "config", "--global", "--get", "user.name"); r.err == nil {
			t.Fatalf("precondition: user.name is set after gh auth setup-git: %q", r.stdout)
		}
		var stdout, stderr bytes.Buffer

		if err := newApp(&stdout, &stderr).Env(t.Context(), home, cmd.FormatJSON); err != nil {
			t.Fatalf("Env: %v", err)
		}

		if got := strings.TrimSpace(sandbox.mustRun(t, "", "git", "config", "--global", "--get", "user.name")); got != "hand-bot[bot]" {
			t.Errorf("user.name = %q", got)
		}
		if got := strings.TrimSpace(sandbox.mustRun(t, "", "git", "config", "--global", "--get", "user.email")); got != "4242+hand-bot[bot]@users.noreply.github.com" {
			t.Errorf("user.email = %q", got)
		}
		if strings.Contains(stderr.String(), "credential helper") {
			t.Errorf("stderr = %q, want no mention of the credential helper, which was already there", stderr.String())
		}
	})
}
