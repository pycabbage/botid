package cmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
)

const (
	testClientID = "Iv23liTEST"
	testToken    = "ghs_secret_token"
)

var generateKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := generateKey()
	if err != nil {
		t.Fatalf("cannot generate a test key: %v", err)
	}
	return key
}

func pkcs1PEM(t *testing.T) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey(t))}))
}

func pkcs8PEM(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

const testSlug = "my-app"

type githubFake struct {
	mu       sync.Mutex
	reqs     []string
	installs []ghapp.Installation
	fail     map[string]int
	slug     string
	tokens   map[int64]string
}

func (g *githubFake) tokenFor(id int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if token, ok := g.tokens[id]; ok {
		return token
	}
	return testToken
}

// acceptsBearer reports whether the Authorization header carries a token that the fake has issued.
func (g *githubFake) acceptsBearer(auth string) bool {
	if auth == "Bearer "+testToken {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, token := range g.tokens {
		if auth == "Bearer "+token {
			return true
		}
	}
	return false
}

func (g *githubFake) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.reqs...)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (g *githubFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	line := r.Method + " " + r.URL.EscapedPath()
	g.mu.Lock()
	g.reqs = append(g.reqs, line)
	status := g.fail[line]
	slug := g.slug
	g.mu.Unlock()

	if status != 0 {
		writeJSON(w, status, `{"message":"Bad credentials"}`)
		return
	}
	auth := r.Header.Get("Authorization")
	switch line {
	case "GET /app/installations":
		items := make([]string, 0, len(g.installs))
		for _, it := range g.installs {
			items = append(items, fmt.Sprintf(`{"id":%d,"suspended_at":null,"account":{"login":%q}}`, it.ID, it.Login))
		}
		writeJSON(w, 200, "["+strings.Join(items, ",")+"]")
	case "GET /app":
		writeJSON(w, 200, `{"slug":"`+slug+`"}`)
	case "GET /users/" + slug + "%5Bbot%5D":
		if !g.acceptsBearer(auth) {
			writeJSON(w, 401, `{"message":"Bad credentials"}`)
			return
		}
		writeJSON(w, 200, `{"id":123456}`)
	default:
		for _, it := range g.installs {
			if line == fmt.Sprintf("POST /app/installations/%d/access_tokens", it.ID) {
				writeJSON(w, 201, `{"token":"`+g.tokenFor(it.ID)+`","expires_at":"2026-10-19T12:00:00Z"}`)
				return
			}
		}
		writeJSON(w, 404, `{"message":"Not Found"}`)
	}
}

type call struct {
	Env  []string
	Name string
	Args []string
	Ctx  any
}

func (c call) line() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

type recorder struct {
	mu    sync.Mutex
	calls []call
	errs  map[string]error
	out   map[string]string
	// after runs when an exec call has succeeded.
	after func(c call)
}

func (r *recorder) record(ctx context.Context, env []string, name string, args []string) ([]byte, error) {
	c := call{Env: env, Name: name, Args: args, Ctx: ctx.Value(ctxKey{})}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	return []byte(r.out[c.line()]), r.errs[c.line()]
}

func (r *recorder) exec(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	out, err := r.record(ctx, env, name, args)
	if err == nil && r.after != nil {
		r.after(call{Env: env, Name: name, Args: args})
	}
	return out, err
}

func (r *recorder) systemctl(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.record(ctx, nil, name, args)
}

func (r *recorder) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		out = append(out, c.line())
	}
	return out
}

type harness struct {
	t      *testing.T
	home   string
	app    *cmd.App
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	gh     *githubFake
	rec    *recorder
	git    *fakeGit

	mu        sync.Mutex
	clientIDs []string
	keys      []*rsa.PrivateKey
}

const fakeExe = "/opt/botid/bin/botid"

func newHarness(t *testing.T, stdin string, installs ...ghapp.Installation) *harness {
	t.Helper()
	h := &harness{
		t:      t,
		home:   t.TempDir(),
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		gh:     &githubFake{installs: installs, fail: map[string]int{}, slug: testSlug, tokens: map[int64]string{}},
		rec:    &recorder{errs: map[string]error{}, out: map[string]string{}},
		git:    &fakeGit{out: map[string]string{}, errs: map[string]error{}},
	}
	h.rec.after = h.applyGitCall
	srv := httptest.NewServer(h.gh)
	t.Cleanup(srv.Close)

	h.app = &cmd.App{
		Home:   h.home,
		Stdin:  strings.NewReader(stdin),
		Stdout: h.stdout,
		Stderr: h.stderr,
		Client: func(clientID string, key *rsa.PrivateKey) *ghapp.Client {
			h.mu.Lock()
			h.clientIDs = append(h.clientIDs, clientID)
			h.keys = append(h.keys, key)
			h.mu.Unlock()
			return &ghapp.Client{BaseURL: srv.URL, ClientID: clientID, Key: key}
		},
		Exec:    h.rec.exec,
		Git:     h.git.run,
		Systemd: h.rec.systemctl,
		Exe:     func() (string, error) { return fakeExe, nil },
	}
	return h
}

const (
	gitHelperValue = "!/usr/bin/gh auth git-credential"
	botEmail       = "123456+my-app[bot]@users.noreply.github.com"
)

// gitListing is what git config --list -z prints for a config file with the given settings.
// name and email are the values, and an empty value means the setting is absent.
func gitListing(name, email string, helper bool) string {
	var b strings.Builder
	if name != "" {
		b.WriteString("user.name\n" + name + "\x00")
	}
	if email != "" {
		b.WriteString("user.email\n" + email + "\x00")
	}
	if helper {
		b.WriteString("credential.https://github.com.helper\n\x00")
		b.WriteString("credential.https://github.com.helper\n" + gitHelperValue + "\x00")
	}
	return b.String()
}

var completeGitListing = gitListing("my-app[bot]", botEmail, true)

func (h *harness) identityDataPath(name string, elem ...string) string {
	return filepath.Join(append([]string{h.app.Home, ".local", "share", "botid", "identities", name}, elem...)...)
}

func (h *harness) gitConfigPath(name string) string {
	return h.identityDataPath(name, "git", "config")
}

// setGitListing makes the fake git report listing for the git config file of the identity.
func (h *harness) setGitListing(g *fakeGit, name, listing string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.out["config --file "+h.gitConfigPath(name)+" --list -z"] = listing
}

func (h *harness) writeFile(path, content string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// writeGitConfigFile creates the git config file of the identity, which git then reports as listing.
func (h *harness) writeGitConfigFile(g *fakeGit, name, listing string) {
	h.t.Helper()
	h.writeFile(h.gitConfigPath(name), "[user]\n")
	h.setGitListing(g, name, listing)
}

// applyGitCall does to the git config file what a git or gh command of botid does, so that a later read finds the setting.
// The git config file is the one in GIT_CONFIG_GLOBAL. A real git appends a value that it sets, and the last value wins.
func (h *harness) applyGitCall(c call) {
	var file string
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, "GIT_CONFIG_GLOBAL="); ok {
			file = v
		}
	}
	line := c.line()
	var entry string
	switch {
	case file == "":
		return
	case strings.HasPrefix(line, "git config --global user.name "):
		entry = "user.name\n" + strings.TrimPrefix(line, "git config --global user.name ") + "\x00"
	case strings.HasPrefix(line, "git config --global user.email "):
		entry = "user.email\n" + strings.TrimPrefix(line, "git config --global user.email ") + "\x00"
	case line == "gh auth setup-git":
		entry = "credential.https://github.com.helper\n\x00credential.https://github.com.helper\n" + gitHelperValue + "\x00"
	default:
		return
	}
	if _, err := os.Stat(file); err != nil {
		h.writeFile(file, "[user]\n")
	}
	h.git.mu.Lock()
	defer h.git.mu.Unlock()
	h.git.out["config --file "+file+" --list -z"] += entry
}

// markGitComplete gives each identity a git config file that has every setting that botid init writes.
func (h *harness) markGitComplete(g *fakeGit, names ...string) {
	h.t.Helper()
	for _, name := range names {
		h.writeGitConfigFile(g, name, completeGitListing)
	}
}

// markReady gives each identity what botid init leaves: a hosts.yml and a complete git config file.
func (h *harness) markReady(g *fakeGit, names ...string) {
	h.t.Helper()
	for _, name := range names {
		h.writeFile(h.identityDataPath(name, "gh", "hosts.yml"), "hosts of "+name+"\n")
	}
	h.markGitComplete(g, names...)
}

// snapshot returns every file and directory below the home directory with its contents.
func (h *harness) snapshot() map[string]string {
	h.t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(h.app.Home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			files[path] = "<dir>"
			return nil
		}
		b, err := os.ReadFile(path)
		files[path] = string(b)
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return files
}

func (h *harness) path(elem ...string) string {
	return filepath.Join(append([]string{h.home}, elem...)...)
}

func (h *harness) write(rel, content string) {
	h.t.Helper()
	p := h.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) saveConfig(c config.Config) {
	h.t.Helper()
	dir := h.path(".config", "botid")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := config.Save(filepath.Join(dir, "config.json"), c); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(h.path(rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func (h *harness) entries(rel string) []string {
	h.t.Helper()
	ents, err := os.ReadDir(h.path(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func (h *harness) mode(rel string) os.FileMode {
	h.t.Helper()
	info, err := os.Stat(h.path(rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return info.Mode().Perm()
}

func (h *harness) assertHomeUntouched() {
	h.t.Helper()
	if got := h.entries("."); len(got) != 0 {
		h.t.Errorf("%v was created in the home directory; nothing should be written", got)
	}
}

func (h *harness) assertNoOutputLeaksToken() {
	h.t.Helper()
	for name, buf := range map[string]*bytes.Buffer{"stdout": h.stdout, "stderr": h.stderr} {
		if strings.Contains(buf.String(), testToken) {
			h.t.Errorf("%s contains the token: %q", name, buf.String())
		}
	}
}

func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
