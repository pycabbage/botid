package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pycabbage/botid/internal/repo"
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

func pkcs1PEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const sampleJSON = `{
  "default": "acme-bot",
  "identities": {
    "acme-bot": {"clientId": "Iv23liA", "privateKeyPath": "/k/a.pem", "installationId": 11},
    "octocorp-bot": {"clientId": "Iv23liB", "privateKeyPath": "/k/b.pem", "installationId": 22},
    "anyproduct-bot": {"clientId": "Iv23liC", "privateKey": "inline", "installationId": 33}
  },
  "rules": [
    {"remote": "github.com/octocorp/*", "identity": "octocorp-bot"},
    {"remote": "github.com/*/anyproduct-*", "identity": "anyproduct-bot"},
    {"path": "~/src/private/**", "identity": "user"},
    {"remote": "github.com/octocorp/*", "branch": "release/**", "identity": "user"}
  ]
}`

func sampleConfig() Config {
	return Config{
		Default: "acme-bot",
		Identities: map[string]Identity{
			"acme-bot":       {ClientID: "Iv23liA", PrivateKeyPath: "/k/a.pem", InstallationID: 11},
			"octocorp-bot":   {ClientID: "Iv23liB", PrivateKeyPath: "/k/b.pem", InstallationID: 22},
			"anyproduct-bot": {ClientID: "Iv23liC", PrivateKey: "inline", InstallationID: 33},
		},
		Rules: []Rule{
			{Remote: "github.com/octocorp/*", Identity: "octocorp-bot"},
			{Remote: "github.com/*/anyproduct-*", Identity: "anyproduct-bot"},
			{Path: "~/src/private/**", Identity: "user"},
			{Remote: "github.com/octocorp/*", Branch: "release/**", Identity: "user"},
		},
	}
}

func TestLoadReadsEveryField(t *testing.T) {
	got, err := Load(writeConfig(t, sampleJSON))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := sampleConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   Config
	}{
		{"full", sampleConfig()},
		{"no rules", Config{
			Default:    "bot",
			Identities: map[string]Identity{"bot": {ClientID: "Iv23liABC", PrivateKeyPath: "/k.pem", InstallationID: 42}},
		}},
		{"installationId unset", Config{
			Default:    "bot",
			Identities: map[string]Identity{"bot": {ClientID: "Iv23liABC", PrivateKeyPath: "/k.pem"}},
		}},
		{"default is user", Config{
			Default:    "user",
			Identities: map[string]Identity{"bot": {ClientID: "Iv23liABC", PrivateKey: "-----BEGIN-----\nxyz\n", InstallationID: 7}},
			Rules:      []Rule{{Path: "/srv/**", Identity: "bot"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := Save(path, tt.in); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(got, tt.in) {
				t.Errorf("Load = %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestSaveWritesExpectedJSONWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Config{
		Default: "zeta",
		Identities: map[string]Identity{
			"zeta":  {ClientID: "Iv23liZ", PrivateKeyPath: "/k/zeta.pem", InstallationID: 2},
			"alpha": {ClientID: "Iv23liA", PrivateKeyPath: "/k/alpha.pem", InstallationID: 1},
		},
		Rules: []Rule{
			{Remote: "github.com/octocorp/*", Identity: "alpha"},
			{Path: "~/src/**", Branch: "release/**", Identity: "user"},
		},
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "default": "zeta",
  "identities": {
    "alpha": {
      "clientId": "Iv23liA",
      "privateKeyPath": "/k/alpha.pem",
      "installationId": 1
    },
    "zeta": {
      "clientId": "Iv23liZ",
      "privateKeyPath": "/k/zeta.pem",
      "installationId": 2
    }
  },
  "rules": [
    {
      "remote": "github.com/octocorp/*",
      "identity": "alpha"
    },
    {
      "path": "~/src/**",
      "branch": "release/**",
      "identity": "user"
    }
  ]
}
`
	if string(got) != want {
		t.Errorf("config.json =\n%s\nwant\n%s", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveWritesEmptyIdentitiesAsObjectAndOmitsEmptyRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, Config{Default: "user"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"default\": \"user\",\n  \"identities\": {}\n}\n"
	if string(got) != want {
		t.Errorf("config.json =\n%s\nwant\n%s", got, want)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("Load of the saved file: %v", err)
	}
}

func TestSaveReplacesExistingFilePermission(t *testing.T) {
	path := writeConfig(t, "old")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{Default: "user"}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadMissingFileIsErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	_, err := Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadDoesNotRequireInstallationID(t *testing.T) {
	path := writeConfig(t, `{"default":"bot","identities":{"bot":{"clientId":"Iv23liABC","privateKeyPath":"/k.pem"}}}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Identities["bot"].InstallationID != 0 {
		t.Errorf("InstallationID = %d, want 0", got.Identities["bot"].InstallationID)
	}
}

func TestLoadAcceptsUserAsDefaultWithoutIdentities(t *testing.T) {
	got, err := Load(writeConfig(t, `{"default":"user","identities":{}}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Default != User || len(got.Identities) != 0 {
		t.Errorf("Load = %+v", got)
	}
}

func TestLoadRejects(t *testing.T) {
	const bot = `"bot":{"clientId":"id","privateKeyPath":"/k.pem","installationId":1}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty default", `{"default":"","identities":{` + bot + `}}`, "default is empty"},
		{"missing default", `{"identities":{` + bot + `}}`, "default is empty"},
		{"unknown default", `{"default":"nobody","identities":{` + bot + `}}`, `default refers to the unknown identity "nobody"`},
		{"empty clientId", `{"default":"bot","identities":{"bot":{"clientId":"","privateKeyPath":"/k.pem"}}}`, `identity "bot": clientId is empty`},
		{"missing clientId", `{"default":"bot","identities":{"bot":{"privateKeyPath":"/k.pem"}}}`, `identity "bot": clientId is empty`},
		{"both key sources", `{"default":"bot","identities":{"bot":{"clientId":"id","privateKey":"x","privateKeyPath":"/k.pem"}}}`, "both privateKey and privateKeyPath are set"},
		{"neither key source", `{"default":"bot","identities":{"bot":{"clientId":"id"}}}`, "either privateKey or privateKeyPath is required"},
		{"both key sources empty", `{"default":"bot","identities":{"bot":{"clientId":"id","privateKey":"","privateKeyPath":""}}}`, "either privateKey or privateKeyPath is required"},
		{"user as identity name", `{"default":"user","identities":{"user":{"clientId":"id","privateKeyPath":"/k.pem"}}}`, `identity name "user" is reserved`},
		{"identity name with slash", `{"default":"a/b","identities":{"a/b":{"clientId":"id","privateKeyPath":"/k.pem"}}}`, `identity name "a/b" must match`},
		{"identity name starting with dot", `{"default":".x","identities":{".x":{"clientId":"id","privateKeyPath":"/k.pem"}}}`, `identity name ".x" must match`},
		{"empty identity name", `{"default":"user","identities":{"":{"clientId":"id","privateKeyPath":"/k.pem"}}}`, `identity name "" must match`},
		{"rule with unknown identity", `{"default":"bot","identities":{` + bot + `},"rules":[{"remote":"github.com/*","identity":"nobody"}]}`, `rules[0]: identity refers to the unknown identity "nobody"`},
		{"rule without identity", `{"default":"bot","identities":{` + bot + `},"rules":[{"remote":"github.com/*"}]}`, `rules[0]: identity refers to the unknown identity ""`},
		{"rule with only a branch", `{"default":"bot","identities":{` + bot + `},"rules":[{"branch":"main","identity":"bot"}]}`, "rules[0]: a rule needs path or remote"},
		{"rule without any pattern", `{"default":"bot","identities":{` + bot + `},"rules":[{"identity":"bot"}]}`, "rules[0]: a rule needs path or remote"},
		{"second rule is invalid", `{"default":"bot","identities":{` + bot + `},"rules":[{"remote":"a/*","identity":"bot"},{"identity":"bot"}]}`, "rules[1]: a rule needs path or remote"},
		{"relative path pattern", `{"default":"bot","identities":{` + bot + `},"rules":[{"path":"src/**","identity":"bot"}]}`, `rules[0]: path: pattern "src/**" must start with / or ~/`},
		{"path pattern with ~user", `{"default":"bot","identities":{` + bot + `},"rules":[{"path":"~alice/src","identity":"bot"}]}`, `must start with / or ~/`},
		{"bad path glob", `{"default":"bot","identities":{` + bot + `},"rules":[{"path":"/src/[","identity":"bot"}]}`, "rules[0]: path:"},
		{"bad remote glob", `{"default":"bot","identities":{` + bot + `},"rules":[{"remote":"github.com/[x","identity":"bot"}]}`, "rules[0]: remote:"},
		{"bad branch glob", `{"default":"bot","identities":{` + bot + `},"rules":[{"remote":"a/*","branch":"a//b","identity":"bot"}]}`, "rules[0]: branch:"},
		{"unknown field", `{"default":"bot","identities":{` + bot + `},"extra":true}`, `unknown field "extra"`},
		{"unknown field in a rule", `{"default":"bot","identities":{` + bot + `},"rules":[{"remote":"a/*","identity":"bot","note":"x"}]}`, `unknown field "note"`},
		{"not JSON", `default=bot`, "invalid character"},
		{"empty file", ``, "EOF"},
		{"wrong type", `{"default":"bot","identities":{"bot":{"clientId":"id","privateKeyPath":"/k.pem","installationId":"1"}}}`, "installationId"},
		{"trailing content", `{"default":"user","identities":{}} {}`, "unexpected extra content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.body)
			got, err := Load(path)
			if err == nil {
				t.Fatalf("want error, got result: %+v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to contain config.json path %q", err, path)
			}
			if errors.Is(err, ErrLegacy) {
				t.Errorf("error = %v, must not be ErrLegacy", err)
			}
		})
	}
}

func TestLoadReportsV010FileAsLegacy(t *testing.T) {
	for _, body := range []string{
		`{"clientId":"Iv23liABC","privateKeyPath":"/home/alice/.config/botid/private-key.pem","installationId":42}`,
		`{"clientId":"Iv23liABC","privateKey":"x","installationId":42}`,
		`{"clientId":""}`,
	} {
		path := writeConfig(t, body)
		_, err := Load(path)
		if !errors.Is(err, ErrLegacy) {
			t.Fatalf("Load(%s) error = %v, want ErrLegacy", body, err)
		}
		want := path + " was written by botid v0.1.0"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestLoadLegacyReadsTheAppOfBotidV010(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Identity
	}{
		{
			"key file",
			`{"clientId":"Iv23liABC","privateKeyPath":"/home/alice/.config/botid/private-key.pem","installationId":42}`,
			Identity{ClientID: "Iv23liABC", PrivateKeyPath: "/home/alice/.config/botid/private-key.pem", InstallationID: 42},
		},
		{
			"inline key",
			`{"clientId":"Iv23liABC","privateKey":"-----BEGIN-----\nxyz\n","installationId":7}`,
			Identity{ClientID: "Iv23liABC", PrivateKey: "-----BEGIN-----\nxyz\n", InstallationID: 7},
		},
		{
			"installationId unset",
			`{"clientId":"Iv23liABC","privateKeyPath":"/k.pem"}`,
			Identity{ClientID: "Iv23liABC", PrivateKeyPath: "/k.pem"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadLegacy(writeConfig(t, tt.body))
			if err != nil {
				t.Fatalf("LoadLegacy: %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadLegacy = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadLegacyRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty clientId", `{"clientId":"","privateKeyPath":"/k.pem"}`, "clientId is empty"},
		{"both key sources", `{"clientId":"id","privateKey":"x","privateKeyPath":"/k.pem"}`, "both privateKey and privateKeyPath are set"},
		{"neither key source", `{"clientId":"id"}`, "either privateKey or privateKeyPath is required"},
		{"unknown field", `{"clientId":"id","privateKeyPath":"/k.pem","extra":1}`, `unknown field "extra"`},
		{"trailing content", `{"clientId":"id","privateKeyPath":"/k.pem"} {}`, "unexpected extra content"},
		{"not JSON", `clientId=id`, "invalid character"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.body)
			got, err := LoadLegacy(path)
			if err == nil {
				t.Fatalf("want error, got result: %+v", got)
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want substring %q and the path %q", err, tt.want, path)
			}
		})
	}
}

func TestLoadLegacyWithMissingFile(t *testing.T) {
	_, err := LoadLegacy(filepath.Join(t.TempDir(), "config.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"a", "my-app", "My_App.2", "0day", "a.b-c_d"} {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", ".hidden", "-x", "_x", "a/b", `a\b`, "a b", "a\nb", "日本語", "user"} {
		if err := ValidName(name); err == nil {
			t.Errorf("ValidName(%q) = nil, want error", name)
		}
	}
}

func TestAppNamesAreSorted(t *testing.T) {
	got := sampleConfig().AppNames()
	want := []string{"acme-bot", "anyproduct-bot", "octocorp-bot"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AppNames = %v, want %v", got, want)
	}
	if got := (Config{Default: "user"}).AppNames(); len(got) != 0 {
		t.Errorf("AppNames of a config without Apps = %v, want none", got)
	}
}

func TestSelect(t *testing.T) {
	home := "/home/alice"
	c := sampleConfig()
	tests := []struct {
		name string
		info repo.Info
		want string
	}{
		{"remote rule", repo.Info{Path: "/work/api", Branch: "main", Remotes: []string{"github.com/octocorp/api"}}, "octocorp-bot"},
		{"branch rule beats the remote rule that comes first", repo.Info{Path: "/work/api", Branch: "release/1.0", Remotes: []string{"github.com/octocorp/api"}}, "user"},
		{"branch glob crosses slashes with **", repo.Info{Path: "/work/api", Branch: "release/1.0/rc", Remotes: []string{"github.com/octocorp/api"}}, "user"},
		{"branch that does not match falls back to the remote rule", repo.Info{Path: "/work/api", Branch: "feature/x", Remotes: []string{"github.com/octocorp/api"}}, "octocorp-bot"},
		{"detached HEAD ignores branch rules", repo.Info{Path: "/work/api", Remotes: []string{"github.com/octocorp/api"}}, "octocorp-bot"},
		{"second remote rule", repo.Info{Path: "/work/web", Branch: "main", Remotes: []string{"github.com/someone/anyproduct-web"}}, "anyproduct-bot"},
		{"overlapping rules take file order", repo.Info{Path: "/work/core", Branch: "main", Remotes: []string{"github.com/octocorp/anyproduct-core"}}, "octocorp-bot"},
		{"path rule with ~", repo.Info{Path: "/home/alice/src/private/notes", Branch: "main", Remotes: []string{"github.com/me/notes"}}, "user"},
		{"path rule needs one segment below the prefix", repo.Info{Path: "/home/alice/src/private", Branch: "main", Remotes: []string{"github.com/me/notes"}}, "acme-bot"},
		{"no rule matches", repo.Info{Path: "/work/bar", Branch: "main", Remotes: []string{"github.com/foo/bar"}}, "acme-bot"},
		{"no remotes", repo.Info{Path: "/work/local", Branch: "main"}, "acme-bot"},
		{"any remote may match", repo.Info{Path: "/work/fork", Branch: "main", Remotes: []string{"github.com/me/fork", "github.com/octocorp/api"}}, "octocorp-bot"},
		{"outside a repository", repo.Info{}, "acme-bot"},
		{"outside a repository ignores a branch", repo.Info{Branch: "release/1"}, "acme-bot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Select(tt.info, home); got != tt.want {
				t.Errorf("Select = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectWithoutRulesUsesTheDefault(t *testing.T) {
	c := Config{Default: "user"}
	info := repo.Info{Path: "/work/api", Branch: "main", Remotes: []string{"github.com/octocorp/api"}}
	if got := c.Select(info, "/home/alice"); got != "user" {
		t.Errorf("Select = %q, want user", got)
	}
}

func TestSelectRuleWithPathAndRemoteNeedsBoth(t *testing.T) {
	c := Config{
		Default: "user",
		Rules:   []Rule{{Path: "/work/**", Remote: "github.com/octocorp/*", Identity: "bot"}},
	}
	tests := []struct {
		name string
		info repo.Info
		want string
	}{
		{"both match", repo.Info{Path: "/work/api", Remotes: []string{"github.com/octocorp/api"}}, "bot"},
		{"only path matches", repo.Info{Path: "/work/api", Remotes: []string{"github.com/other/api"}}, "user"},
		{"only remote matches", repo.Info{Path: "/srv/api", Remotes: []string{"github.com/octocorp/api"}}, "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Select(tt.info, "/home/alice"); got != tt.want {
				t.Errorf("Select = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectRuleWithPathRemoteAndBranchNeedsAll(t *testing.T) {
	c := Config{
		Default: "user",
		Rules:   []Rule{{Path: "/work/**", Remote: "github.com/octocorp/*", Branch: "main", Identity: "bot"}},
	}
	match := repo.Info{Path: "/work/api", Branch: "main", Remotes: []string{"github.com/octocorp/api"}}
	if got := c.Select(match, "/home/alice"); got != "bot" {
		t.Errorf("Select = %q, want bot", got)
	}
	match.Branch = "dev"
	if got := c.Select(match, "/home/alice"); got != "user" {
		t.Errorf("Select with another branch = %q, want user", got)
	}
}

func TestSelectRemoteRuleIgnoresCaseOfThePattern(t *testing.T) {
	c := Config{
		Default: "user",
		Rules:   []Rule{{Remote: "GitHub.com/OctoCorp/*", Identity: "bot"}},
	}
	info := repo.Info{Path: "/work/api", Remotes: []string{"github.com/octocorp/api"}}
	if got := c.Select(info, "/home/alice"); got != "bot" {
		t.Errorf("Select = %q, want bot", got)
	}
}

func TestSelectBranchAndPathPatternsAreCaseSensitive(t *testing.T) {
	c := Config{
		Default: "user",
		Rules: []Rule{
			{Path: "/Work/**", Identity: "path-bot"},
			{Remote: "github.com/*/*", Branch: "Release", Identity: "branch-bot"},
		},
	}
	info := repo.Info{Path: "/work/api", Branch: "release", Remotes: []string{"github.com/o/r"}}
	if got := c.Select(info, "/home/alice"); got != "user" {
		t.Errorf("Select = %q, want user", got)
	}
}

func TestSelectRuleWithPathAndBranchNeedsBoth(t *testing.T) {
	c := Config{
		Default: "user",
		Rules:   []Rule{{Path: "/work/**", Branch: "main", Identity: "bot"}},
	}
	if got := c.Select(repo.Info{Path: "/srv/api", Branch: "main"}, "/home/alice"); got != "user" {
		t.Errorf("Select = %q, want user", got)
	}
	if got := c.Select(repo.Info{Path: "/work/api", Branch: "main"}, "/home/alice"); got != "bot" {
		t.Errorf("Select = %q, want bot", got)
	}
}

func TestSelectPathRuleResolvesSymlinksOnBothSides(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	t.Run("pattern written through a symlink matches the real path", func(t *testing.T) {
		c := Config{Default: "user", Rules: []Rule{{Path: link + "/**", Identity: "bot"}}}
		if got := c.Select(repo.Info{Path: filepath.Join(real, "proj")}, "/home/alice"); got != "bot" {
			t.Errorf("Select = %q, want bot", got)
		}
	})
	t.Run("pattern written with the real path matches a repository reached through a symlink", func(t *testing.T) {
		c := Config{Default: "user", Rules: []Rule{{Path: real + "/**", Identity: "bot"}}}
		if got := c.Select(repo.Info{Path: filepath.Join(link, "proj")}, "/home/alice"); got != "bot" {
			t.Errorf("Select = %q, want bot", got)
		}
	})
	t.Run("a pattern whose prefix does not exist is used as written", func(t *testing.T) {
		c := Config{Default: "user", Rules: []Rule{{Path: "/no/such/prefix/**", Identity: "bot"}}}
		if got := c.Select(repo.Info{Path: "/no/such/prefix/proj"}, "/home/alice"); got != "bot" {
			t.Errorf("Select = %q, want bot", got)
		}
	})
	t.Run("a pattern for another directory does not match", func(t *testing.T) {
		c := Config{Default: "user", Rules: []Rule{{Path: filepath.Join(base, "other") + "/**", Identity: "bot"}}}
		if got := c.Select(repo.Info{Path: filepath.Join(real, "proj")}, "/home/alice"); got != "user" {
			t.Errorf("Select = %q, want user", got)
		}
	})
}

func TestSelectPathRuleExpandsTildeWithTheGivenHome(t *testing.T) {
	c := Config{Default: "user", Rules: []Rule{{Path: "~/src/*", Identity: "bot"}}}
	if got := c.Select(repo.Info{Path: "/srv/alice/src/api"}, "/srv/alice/"); got != "bot" {
		t.Errorf("Select = %q, want bot", got)
	}
	if got := c.Select(repo.Info{Path: "/home/alice/src/api"}, "/srv/alice"); got != "user" {
		t.Errorf("Select with another home = %q, want user", got)
	}
}

func TestKeyReadsPKCS1AndPKCS8FromPathAndInline(t *testing.T) {
	key := testKey(t)
	for _, format := range []struct {
		name string
		pem  string
	}{
		{"PKCS#1", pkcs1PEM(key)},
		{"PKCS#8", pkcs8PEM(t, key)},
	} {
		t.Run(format.name+" (privateKeyPath)", func(t *testing.T) {
			keyPath := filepath.Join(t.TempDir(), "key.pem")
			if err := os.WriteFile(keyPath, []byte(format.pem), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Identity{ClientID: "id", PrivateKeyPath: keyPath}.Key()
			if err != nil {
				t.Fatalf("Key: %v", err)
			}
			if !got.Equal(key) {
				t.Error("loaded key does not match the original key")
			}
		})
		t.Run(format.name+" (privateKey)", func(t *testing.T) {
			got, err := Identity{ClientID: "id", PrivateKey: format.pem}.Key()
			if err != nil {
				t.Fatalf("Key: %v", err)
			}
			if !got.Equal(key) {
				t.Error("loaded key does not match the original key")
			}
		})
	}
}

func TestKeyErrorsNameThePathButNeverTheKeyContents(t *testing.T) {
	key := testKey(t)
	body := pkcs1PEM(key)
	bodyLines := strings.Split(strings.TrimSpace(body), "\n")
	secretLine := bodyLines[1]

	t.Run("broken PEM (privateKeyPath)", func(t *testing.T) {
		keyPath := filepath.Join(t.TempDir(), "key.pem")
		broken := "-----BEGIN RSA PRIVATE KEY-----\n" + secretLine + "\n-----END RSA PRIVATE KEY-----\n"
		if err := os.WriteFile(keyPath, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Identity{ClientID: "id", PrivateKeyPath: keyPath}.Key()
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), keyPath) {
			t.Errorf("error = %q, want it to contain key path %q", err, keyPath)
		}
		if strings.Contains(err.Error(), secretLine) {
			t.Errorf("error contains key contents: %q", err)
		}
	})

	t.Run("broken PEM (privateKey)", func(t *testing.T) {
		broken := "-----BEGIN RSA PRIVATE KEY-----\n" + secretLine + "\n-----END RSA PRIVATE KEY-----\n"
		_, err := Identity{ClientID: "id", PrivateKey: broken}.Key()
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), secretLine) {
			t.Errorf("error contains key contents: %q", err)
		}
	})

	t.Run("non-PEM string (privateKey)", func(t *testing.T) {
		const secret = "super-secret-not-a-pem"
		_, err := Identity{ClientID: "id", PrivateKey: secret}.Key()
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error contains key contents: %q", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		keyPath := filepath.Join(t.TempDir(), "missing.pem")
		_, err := Identity{ClientID: "id", PrivateKeyPath: keyPath}.Key()
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("error = %v, want fs.ErrNotExist", err)
		}
		if !strings.Contains(err.Error(), keyPath) {
			t.Errorf("error = %q, want it to contain key path %q", err, keyPath)
		}
	})
}

func TestKeyRejectsEncryptedPEM(t *testing.T) {
	encrypted := string(pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00112233445566778899AABBCCDDEEFF"},
		Bytes:   []byte("dummy"),
	}))
	_, err := Identity{ClientID: "id", PrivateKey: encrypted}.Key()
	if err == nil || !strings.Contains(err.Error(), "encrypted private keys are not supported") {
		t.Fatalf("error = %v, want unsupported encrypted PEM error", err)
	}
}

func TestKeyRejectsInvalidKeySource(t *testing.T) {
	tests := []struct {
		name string
		i    Identity
		want string
	}{
		{"both", Identity{ClientID: "id", PrivateKey: "x", PrivateKeyPath: "/k.pem"}, "both privateKey and privateKeyPath are set"},
		{"neither", Identity{ClientID: "id"}, "either privateKey or privateKeyPath is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.i.Key()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
