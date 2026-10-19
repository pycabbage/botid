package ghapp_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pycabbage/botid/internal/ghapp"
)

var generateKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := generateKey()
	if err != nil {
		t.Fatalf("cannot generate test key: %v", err)
	}
	return key
}

var fixedNow = time.Unix(1_700_000_000, 0)

type seen struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

func (s seen) String() string { return s.Method + " " + s.Path }

type fake struct {
	mu   sync.Mutex
	reqs []seen
}

func (f *fake) all() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen(nil), f.reqs...)
}

func (f *fake) lines() []string {
	var out []string
	for _, r := range f.all() {
		out = append(out, r.String())
	}
	return out
}

func (f *fake) count(line string) int {
	n := 0
	for _, l := range f.lines() {
		if l == line {
			n++
		}
	}
	return n
}

func (f *fake) last(t *testing.T, line string) seen {
	t.Helper()
	all := f.all()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].String() == line {
			return all[i]
		}
	}
	t.Fatalf("request %q was not recorded; recorded: %v", line, f.lines())
	return seen{}
}

func (f *fake) jwtFor(t *testing.T, line string) string {
	t.Helper()
	jwt := strings.TrimPrefix(f.last(t, line).Header.Get("Authorization"), "Bearer ")
	if jwt == "" {
		t.Fatalf("%q has no JWT", line)
	}
	return jwt
}

func newClient(t *testing.T, handler http.HandlerFunc) (*ghapp.Client, *fake) {
	t.Helper()
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("cannot read request body: %v", err)
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{
			Method: r.Method,
			Path:   r.URL.EscapedPath(),
			Query:  r.URL.RawQuery,
			Header: r.Header.Clone(),
			Body:   string(body),
		})
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return &ghapp.Client{
		BaseURL:  srv.URL,
		Now:      func() time.Time { return fixedNow },
		ClientID: "Iv23liTESTCLIENT",
		Key:      testKey(t),
	}, f
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func verifyJWT(t *testing.T, jwt string, pub *rsa.PublicKey) {
	t.Helper()
	if strings.Contains(jwt, "=") {
		t.Errorf("JWT contains base64 padding: %q", jwt)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT should have 3 parts but has %d", len(parts))
	}
	enc := base64.RawURLEncoding

	headerJSON, err := enc.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("cannot decode header as base64url: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("cannot parse header as JSON: %v", err)
	}
	if want := map[string]string{"alg": "RS256", "typ": "JWT"}; !reflect.DeepEqual(header, want) {
		t.Errorf("header = %v, want %v", header, want)
	}

	claimsJSON, err := enc.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("cannot decode payload as base64url: %v", err)
	}
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("cannot parse payload as JSON with integer seconds: %v (%s)", err, claimsJSON)
	}
	if claims.Iss != "Iv23liTESTCLIENT" {
		t.Errorf("iss = %q, want %q", claims.Iss, "Iv23liTESTCLIENT")
	}
	if claims.Iat != 1_699_999_940 {
		t.Errorf("iat = %d, want %d", claims.Iat, 1_699_999_940)
	}
	if claims.Exp != 1_700_000_540 {
		t.Errorf("exp = %d, want %d", claims.Exp, 1_700_000_540)
	}

	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("cannot decode signature as base64url: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("cannot verify signature with public key: %v", err)
	}
}

func installationJSON(id int64, login string, suspended bool) string {
	suspendedAt := "null"
	if suspended {
		suspendedAt = `"2026-01-01T00:00:00Z"`
	}
	return fmt.Sprintf(`{"id":%d,"suspended_at":%s,"account":{"login":%q}}`, id, suspendedAt, login)
}

func issueHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /app/installations/42/access_tokens":
			writeJSON(w, 201, `{"token":"ghs_secret_token","expires_at":"2026-10-19T12:00:00Z"}`)
		case "GET /app":
			writeJSON(w, 200, `{"slug":"my-app"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			writeJSON(w, 404, `{"message":"Not Found"}`)
		}
	}
}

func TestJWTIsSignedAndHasExpectedClaims(t *testing.T) {
	client, f := newClient(t, issueHandler(t))

	if _, err := client.Issue(context.Background(), 42); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, line := range []string{"POST /app/installations/42/access_tokens", "GET /app"} {
		t.Run(line, func(t *testing.T) {
			verifyJWT(t, f.jwtFor(t, line), &testKey(t).PublicKey)
		})
	}
}

func TestRequestsCarryCommonHeaders(t *testing.T) {
	client, f := newClient(t, issueHandler(t))

	if _, err := client.Issue(context.Background(), 42); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, r := range f.all() {
		t.Run(r.String(), func(t *testing.T) {
			want := map[string]string{
				"Accept":               "application/vnd.github+json",
				"User-Agent":           "botid",
				"X-Github-Api-Version": "2022-11-28",
			}
			for k, v := range want {
				if got := r.Header.Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Errorf("Authorization = %q, should start with Bearer", r.Header.Get("Authorization"))
			}
		})
	}
}

func TestInstallations(t *testing.T) {
	tests := []struct {
		name  string
		pages [][]string
		want  []ghapp.Installation
	}{
		{
			name:  "0 installations",
			pages: [][]string{{}},
			want:  []ghapp.Installation{},
		},
		{
			name:  "1 installation",
			pages: [][]string{{installationJSON(7, "alice", false)}},
			want:  []ghapp.Installation{{ID: 7, Login: "alice"}},
		},
		{
			name: "2 installations",
			pages: [][]string{{
				installationJSON(7, "alice", false),
				installationJSON(8, "acme-org", false),
			}},
			want: []ghapp.Installation{{ID: 7, Login: "alice"}, {ID: 8, Login: "acme-org"}},
		},
		{
			name: "excludes suspended",
			pages: [][]string{{
				installationJSON(1, "active-a", false),
				installationJSON(2, "suspended", true),
				installationJSON(3, "active-b", false),
			}},
			want: []ghapp.Installation{{ID: 1, Login: "active-a"}, {ID: 3, Login: "active-b"}},
		},
		{
			name: "only suspended",
			pages: [][]string{{
				installationJSON(2, "suspended", true),
			}},
			want: []ghapp.Installation{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method+" "+r.URL.Path != "GET /app/installations" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				writeJSON(w, 200, "["+strings.Join(tt.pages[0], ",")+"]")
			})

			got, err := client.Installations(context.Background())
			if err != nil {
				t.Fatalf("Installations: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("result = %+v, want %+v", got, tt.want)
			}
			if want := []string{"GET /app/installations"}; !reflect.DeepEqual(f.lines(), want) {
				t.Errorf("requests = %v, want %v", f.lines(), want)
			}
			if q := f.all()[0].Query; q != "per_page=100&page=1" {
				t.Errorf("query = %q, want %q", q, "per_page=100&page=1")
			}
		})
	}
}

func TestInstallationsPaginatesUntilShortPage(t *testing.T) {
	page := func(from, to int64) string {
		var items []string
		for id := from; id <= to; id++ {
			items = append(items, installationJSON(id, fmt.Sprintf("org%d", id), false))
		}
		return "[" + strings.Join(items, ",") + "]"
	}

	tests := []struct {
		name        string
		pages       map[string]string
		wantQueries []string
		wantCount   int
		wantLastID  int64
	}{
		{
			name:        "100 installations followed by 2",
			pages:       map[string]string{"1": page(1, 100), "2": page(101, 102)},
			wantQueries: []string{"per_page=100&page=1", "per_page=100&page=2"},
			wantCount:   102,
			wantLastID:  102,
		},
		{
			name:        "exactly 100 installations followed by an empty page",
			pages:       map[string]string{"1": page(1, 100), "2": "[]"},
			wantQueries: []string{"per_page=100&page=1", "per_page=100&page=2"},
			wantCount:   100,
			wantLastID:  100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, ok := tt.pages[r.URL.Query().Get("page")]
				if !ok {
					t.Errorf("unexpected page: %s", r.URL)
					writeJSON(w, 404, `{"message":"Not Found"}`)
					return
				}
				writeJSON(w, 200, body)
			})

			got, err := client.Installations(context.Background())
			if err != nil {
				t.Fatalf("Installations: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("count = %d, want %d", len(got), tt.wantCount)
			}
			last := got[len(got)-1]
			if last.ID != tt.wantLastID || last.Login != fmt.Sprintf("org%d", tt.wantLastID) {
				t.Errorf("last item = %+v, want {ID:%d Login:org%d}", last, tt.wantLastID, tt.wantLastID)
			}
			var queries []string
			for _, r := range f.all() {
				queries = append(queries, r.Query)
			}
			if !reflect.DeepEqual(queries, tt.wantQueries) {
				t.Errorf("queries = %v, want %v", queries, tt.wantQueries)
			}
		})
	}
}

func TestInstallationsRejectsMalformedResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"not an array", `{"installations":[]}`, "cannot read GitHub response"},
		{"missing id", `[{"suspended_at":null,"account":{"login":"alice"}}]`, "item 1 has no id"},
		{"missing account", `[{"id":5,"suspended_at":null}]`, "installation 5 has no account.login"},
		{"empty login", `[{"id":5,"suspended_at":null,"account":{"login":""}}]`, "installation 5 has no account.login"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, tt.body)
			})

			got, err := client.Installations(context.Background())
			if err == nil {
				t.Fatalf("expected an error but got a result: %+v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestIssueCallsOnlyAccessTokensAndApp(t *testing.T) {
	client, f := newClient(t, issueHandler(t))

	got, err := client.Issue(context.Background(), 42)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	want := ghapp.Credential{
		Token:     "ghs_secret_token",
		Slug:      "my-app",
		ExpiresAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
	}
	if got.Token != want.Token || got.Slug != want.Slug || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("Credential = %+v, want %+v", got, want)
	}

	wantLines := []string{"POST /app/installations/42/access_tokens", "GET /app"}
	if !reflect.DeepEqual(f.lines(), wantLines) {
		t.Errorf("requests = %v, want %v", f.lines(), wantLines)
	}
	if body := f.all()[0].Body; body != "" {
		t.Errorf("access_tokens request body = %q, should be empty", body)
	}
}

func TestIssueRejectsMalformedResponse(t *testing.T) {
	tests := []struct {
		name        string
		accessToken string
		app         string
		want        string
	}{
		{"missing token", `{"expires_at":"2026-10-19T12:00:00Z"}`, `{"slug":"my-app"}`, "response has no token"},
		{"invalid expires_at", `{"token":"ghs_x","expires_at":"tomorrow"}`, `{"slug":"my-app"}`, "cannot read expires_at"},
		{"missing slug", `{"token":"ghs_x","expires_at":"2026-10-19T12:00:00Z"}`, `{}`, "response has no slug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					writeJSON(w, 201, tt.accessToken)
					return
				}
				writeJSON(w, 200, tt.app)
			})

			got, err := client.Issue(context.Background(), 42)
			if err == nil {
				t.Fatalf("expected an error but got a result: %+v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestIssueRejectsNonPositiveInstallationID(t *testing.T) {
	client, f := newClient(t, issueHandler(t))

	_, err := client.Issue(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "botid init") {
		t.Fatalf("error = %v, expected an error that tells the user to run botid init", err)
	}
	if len(f.all()) != 0 {
		t.Errorf("requests were sent: %v", f.lines())
	}
}

func TestBotUserID(t *testing.T) {
	client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":123456,"login":"my-app[bot]"}`)
	})

	got, err := client.BotUserID(context.Background(), "ghs_install_token", "my-app")
	if err != nil {
		t.Fatalf("BotUserID: %v", err)
	}
	if got != 123456 {
		t.Errorf("id = %d, want %d", got, 123456)
	}

	reqs := f.all()
	if len(reqs) != 1 {
		t.Fatalf("request count = %d, want 1 (%v)", len(reqs), f.lines())
	}
	if reqs[0].Method != "GET" || reqs[0].Path != "/users/my-app%5Bbot%5D" {
		t.Errorf("request = %s %s, want GET /users/my-app%%5Bbot%%5D", reqs[0].Method, reqs[0].Path)
	}
	if auth := reqs[0].Header.Get("Authorization"); auth != "Bearer ghs_install_token" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer ghs_install_token")
	}
}

func TestBotUserIDRejectsMalformedResponse(t *testing.T) {
	client, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"login":"my-app[bot]"}`)
	})

	got, err := client.BotUserID(context.Background(), "ghs_install_token", "my-app")
	if err == nil || !strings.Contains(err.Error(), "response has no id") {
		t.Fatalf("result = %d, error = %v, expected an error saying the id is missing", got, err)
	}
}

func TestRetries(t *testing.T) {
	t.Run("5xx is retried until it succeeds", func(t *testing.T) {
		var calls int
		var mu sync.Mutex
		client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if n <= 2 {
					writeJSON(w, 502, `{"message":"Bad Gateway"}`)
					return
				}
			}
			issueHandler(t)(w, r)
		})

		got, err := client.Issue(context.Background(), 42)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if got.Token != "ghs_secret_token" {
			t.Errorf("Token = %q, want %q", got.Token, "ghs_secret_token")
		}
		if n := f.count("POST /app/installations/42/access_tokens"); n != 3 {
			t.Errorf("access_tokens call count = %d, want 3", n)
		}
	})

	t.Run("persistent 5xx stops after the first attempt + 3 retries", func(t *testing.T) {
		client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 503, `{"message":"Service Unavailable"}`)
		})

		_, err := client.Issue(context.Background(), 42)
		if err == nil {
			t.Fatal("expected an error")
		}
		if n := f.count("POST /app/installations/42/access_tokens"); n != 4 {
			t.Errorf("access_tokens call count = %d, want 4 (first attempt + 3 retries)", n)
		}
		if !strings.Contains(err.Error(), "server error (503)") || !strings.Contains(err.Error(), "Service Unavailable") {
			t.Errorf("error = %q", err)
		}
	})

	for _, status := range []int{400, 401, 403, 404, 422, 429} {
		t.Run(fmt.Sprintf("%d is not retried", status), func(t *testing.T) {
			client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, status, `{"message":"nope"}`)
			})

			if _, err := client.Issue(context.Background(), 42); err == nil {
				t.Fatal("expected an error")
			}
			if want := []string{"POST /app/installations/42/access_tokens"}; !reflect.DeepEqual(f.lines(), want) {
				t.Errorf("requests = %v, want %v", f.lines(), want)
			}
		})
	}

	t.Run("network errors are retried", func(t *testing.T) {
		client, f := newClient(t, issueHandler(t))
		var attempts int
		var mu sync.Mutex
		client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if n <= 2 {
				return nil, errors.New("connection reset by peer")
			}
			return http.DefaultTransport.RoundTrip(r)
		})}

		if _, err := client.Issue(context.Background(), 42); err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if attempts != 4 {
			t.Errorf("transport call count = %d, want 4 (2 failures + access_tokens success + /app)", attempts)
		}
		if want := []string{"POST /app/installations/42/access_tokens", "GET /app"}; !reflect.DeepEqual(f.lines(), want) {
			t.Errorf("requests received by server = %v, want %v", f.lines(), want)
		}
	})

	t.Run("persistent network errors stop after the first attempt + 3 retries", func(t *testing.T) {
		client, _ := newClient(t, issueHandler(t))
		var attempts int
		client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("connection refused")
		})}

		_, err := client.Issue(context.Background(), 42)
		if err == nil {
			t.Fatal("expected an error")
		}
		if attempts != 4 {
			t.Errorf("attempt count = %d, want 4", attempts)
		}
		if !strings.Contains(err.Error(), "cannot connect to GitHub API") || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("error = %q", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestErrorMessagesByStatus(t *testing.T) {
	reset := fmt.Sprint(fixedNow.Unix() + 90)

	type call func(*ghapp.Client) error
	installations := call(func(c *ghapp.Client) error {
		_, err := c.Installations(context.Background())
		return err
	})
	issue := call(func(c *ghapp.Client) error {
		_, err := c.Issue(context.Background(), 42)
		return err
	})
	botUserID := call(func(c *ghapp.Client) error {
		_, err := c.BotUserID(context.Background(), "ghs_secret_token", "my-app")
		return err
	})

	tests := []struct {
		name     string
		call     call
		status   int
		headers  map[string]string
		body     string
		wantAll  []string
		wantCall int
	}{
		{
			name: "401 when listing installations", call: installations, status: 401,
			body:    `{"message":"A JSON web token could not be decoded"}`,
			wantAll: []string{"the Client ID or private key may be wrong, or the clock on this PC may be off", "A JSON web token could not be decoded"},
		},
		{
			name: "404 from access_tokens", call: issue, status: 404,
			body:    `{"message":"Not Found"}`,
			wantAll: []string{"the installation may have been deleted", "run botid init again and choose an installation", "Not Found"},
		},
		{
			name: "403 Resource not accessible by integration", call: issue, status: 403,
			body:    `{"message":"Resource not accessible by integration"}`,
			wantAll: []string{"the App does not have enough permissions", "Resource not accessible by integration"},
		},
		{
			name: "403 rate limit", call: issue, status: 403,
			headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset},
			body:    `{"message":"API rate limit exceeded"}`,
			wantAll: []string{"rate limit reached (403)", "in about 90 seconds", "not retrying automatically", "API rate limit exceeded"},
		},
		{
			name: "429 rate limit", call: issue, status: 429,
			headers: map[string]string{"X-RateLimit-Reset": reset},
			body:    `{"message":"Too Many Requests"}`,
			wantAll: []string{"rate limit reached (429)", "in about 90 seconds", "not retrying automatically"},
		},
		{
			name: "429 without reset header", call: issue, status: 429,
			body:    `{"message":"Too Many Requests"}`,
			wantAll: []string{"rate limit reached (429)", "could not determine how long to wait before running again"},
		},
		{
			name: "other 403", call: issue, status: 403,
			body:    `{"message":"Forbidden for some reason"}`,
			wantAll: []string{"access denied (403)", "Forbidden for some reason"},
		},
		{
			name: "500", call: issue, status: 500,
			body:    `{"message":"Server Error"}`,
			wantAll: []string{"server error (500)", "Server Error"},
		},
		{
			name: "500 with a message that is not JSON", call: issue, status: 500,
			body:    `<html>oops</html>`,
			wantAll: []string{"server error (500)"},
		},
		{
			name: "401 from BotUserID", call: botUserID, status: 401,
			body:    `{"message":"Bad credentials"}`,
			wantAll: []string{"installation token is invalid or expired", "Bad credentials"},
		},
		{
			name: "404 from BotUserID", call: botUserID, status: 404,
			body:    `{"message":"Not Found"}`,
			wantAll: []string{"not found (404)", "Not Found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				writeJSON(w, tt.status, tt.body)
			})

			err := tt.call(client)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.wantAll {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want substring %q", err, want)
				}
			}

			for _, r := range f.all() {
				secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if secret == "" {
					t.Fatalf("%s has no Authorization", r)
				}
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error message contains the credential for %s: %q", r, err)
				}
			}
		})
	}
}

func TestErrorAfterTokenIssuedDoesNotLeakTokenOrJWT(t *testing.T) {
	client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, 201, `{"token":"ghs_secret_token","expires_at":"2026-10-19T12:00:00Z"}`)
			return
		}
		writeJSON(w, 401, `{"message":"Bad credentials"}`)
	})

	_, err := client.Issue(context.Background(), 42)
	if err == nil {
		t.Fatal("expected an error")
	}
	jwt := f.jwtFor(t, "GET /app")
	for _, secret := range []string{jwt, "ghs_secret_token"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error message contains a credential: %q", err)
		}
	}
	if !strings.Contains(err.Error(), "getting App info") {
		t.Errorf("error = %q, should contain the name of the failed operation", err)
	}
}

func TestMalformedBodyErrorDoesNotLeakToken(t *testing.T) {
	client, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `ghs_secret_token is not json`)
	})

	_, err := client.BotUserID(context.Background(), "ghs_secret_token", "my-app")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "ghs_secret_token") {
		t.Errorf("error message contains the token: %q", err)
	}
}

func TestInvalidBaseURLIsNotRetriedAsNetworkError(t *testing.T) {
	client, _ := newClient(t, issueHandler(t))
	client.BaseURL = "http://bad host"
	var attempts int
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("unreachable")
	})}

	_, err := client.Issue(context.Background(), 42)
	if err == nil || !strings.Contains(err.Error(), "cannot create request") {
		t.Fatalf("error = %v, expected an error saying the request could not be created", err)
	}
	if attempts != 0 {
		t.Errorf("transport call count = %d, want 0", attempts)
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, f := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		writeJSON(w, 503, `{"message":"Service Unavailable"}`)
	})
	client.RetryDelay = time.Hour

	_, err := client.Issue(ctx, 42)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, expected context.Canceled", err)
	}
	if n := f.count("POST /app/installations/42/access_tokens"); n != 1 {
		t.Errorf("access_tokens call count = %d, want 1", n)
	}
}
