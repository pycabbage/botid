package cmd_test

import (
	"bytes"
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

type githubFake struct {
	mu       sync.Mutex
	reqs     []string
	installs []ghapp.Installation
	fail     map[string]int
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
		writeJSON(w, 200, `{"slug":"my-app"}`)
	case "GET /users/my-app%5Bbot%5D":
		if auth != "Bearer "+testToken {
			writeJSON(w, 401, `{"message":"Bad credentials"}`)
			return
		}
		writeJSON(w, 200, `{"id":123456}`)
	default:
		for _, it := range g.installs {
			if line == fmt.Sprintf("POST /app/installations/%d/access_tokens", it.ID) {
				writeJSON(w, 201, `{"token":"`+testToken+`","expires_at":"2026-10-19T12:00:00Z"}`)
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
}

func (c call) line() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

type recorder struct {
	mu    sync.Mutex
	calls []call
	errs  map[string]error
	out   map[string]string
}

func (r *recorder) record(env []string, name string, args []string) ([]byte, error) {
	c := call{Env: env, Name: name, Args: args}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	return []byte(r.out[c.line()]), r.errs[c.line()]
}

func (r *recorder) exec(env []string, name string, args ...string) ([]byte, error) {
	return r.record(env, name, args)
}

func (r *recorder) systemctl(name string, args ...string) ([]byte, error) {
	return r.record(nil, name, args)
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
		gh:     &githubFake{installs: installs, fail: map[string]int{}},
		rec:    &recorder{errs: map[string]error{}, out: map[string]string{}},
	}
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
		Systemd: h.rec.systemctl,
		Exe:     func() (string, error) { return fakeExe, nil },
	}
	return h
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
