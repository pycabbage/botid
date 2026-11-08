package config

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/glob"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/store"
)

// User is the built-in identity that sets no environment variables, so gh and git behave as the user set them up.
const User = "user"

// ErrLegacy is wrapped by the error that Load returns for a config.json written by botid v0.1.0.
// LoadLegacy reads such a file.
var ErrLegacy = errors.New("was written by botid v0.1.0")

type Identity struct {
	ClientID       string `json:"clientId"`
	PrivateKey     string `json:"privateKey,omitempty"`
	PrivateKeyPath string `json:"privateKeyPath,omitempty"`
	InstallationID int64  `json:"installationId"`
}

type Rule struct {
	Path     string `json:"path,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Identity string `json:"identity"`
}

type Config struct {
	Default    string              `json:"default"`
	Identities map[string]Identity `json:"identities"`
	Rules      []Rule              `json:"rules,omitempty"`
}

// Load reads the config.json at path. If the file does not exist, the error wraps fs.ErrNotExist.
// A file written by botid v0.1.0 gives an error that wraps ErrLegacy.
// An unset installationId (0) is not an error. The caller tells the user to run botid init again.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read config file: %w", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err == nil {
		if _, ok := top["clientId"]; ok {
			return Config{}, fmt.Errorf("%s %w", path, ErrLegacy)
		}
	}

	var c Config
	if err := decodeStrict(path, data, &c); err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// LoadLegacy reads a config.json written by botid v0.1.0. Its top level is the one App identity.
func LoadLegacy(path string) (Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, fmt.Errorf("cannot read config file: %w", err)
	}
	var i Identity
	if err := decodeStrict(path, data, &i); err != nil {
		return Identity{}, err
	}
	if err := i.validate(); err != nil {
		return Identity{}, fmt.Errorf("%s: %w", path, err)
	}
	return i, nil
}

func decodeStrict(path string, data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("cannot read %s: unexpected extra content after JSON", path)
	}
	return nil
}

// Save atomically writes c to path with mode 0600. The caller creates the parent directory.
func Save(path string, c Config) error {
	if c.Identities == nil {
		c.Identities = map[string]Identity{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode config as JSON: %w", err)
	}
	return store.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name can be the key of an App identity. The key is used as a directory name.
func ValidName(name string) error {
	if name == User {
		return fmt.Errorf("identity name %q is reserved for the built-in identity", name)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("identity name %q must match %s", name, namePattern)
	}
	return nil
}

// AppNames returns the names of the App identities in sorted order.
func (c Config) AppNames() []string {
	return slices.Sorted(maps.Keys(c.Identities))
}

// Select returns the name of the identity to use for the directory described by info.
// A matching rule with a branch wins over every rule without one. Among each kind the first match in file order wins.
// Outside a repository there are no remotes and no branch, so only a rule with just a path can match.
// When no rule matches, it returns Default.
func (c Config) Select(info repo.Info, home string) string {
	for _, withBranch := range []bool{true, false} {
		for _, r := range c.Rules {
			if (r.Branch != "") == withBranch && r.matches(info, home) {
				return r.Identity
			}
		}
	}
	return c.Default
}

func (r Rule) matches(info repo.Info, home string) bool {
	if r.Path != "" && !matchPath(r.Path, info.Path, home) {
		return false
	}
	if r.Remote != "" {
		pattern := strings.ToLower(r.Remote)
		if !slices.ContainsFunc(info.Remotes, func(remote string) bool { return glob.Match(pattern, remote) }) {
			return false
		}
	}
	return r.Branch == "" || (info.Branch != "" && glob.Match(r.Branch, info.Branch))
}

// matchPath matches the repository path against a path pattern after it expands a leading "~/"
// and resolves symlinks on both sides, so that a repository matches through any path that reaches it.
func matchPath(pattern, repoPath, home string) bool {
	if rest, ok := strings.CutPrefix(pattern, "~/"); ok {
		pattern = strings.TrimSuffix(home, "/") + "/" + rest
	}
	if resolved, err := filepath.EvalSymlinks(repoPath); err == nil {
		repoPath = resolved
	}
	return glob.Match(resolveLiteralPrefix(pattern), repoPath)
}

// resolveLiteralPrefix resolves symlinks in the leading segments of pattern that contain no glob syntax.
// A prefix that does not exist is left as written.
func resolveLiteralPrefix(pattern string) string {
	segments := strings.Split(pattern, "/")
	n := slices.IndexFunc(segments, func(s string) bool { return strings.ContainsAny(s, `*?[\`) })
	if n < 0 {
		n = len(segments)
	}
	literal := strings.Join(segments[:n], "/")
	if literal == "" {
		return pattern
	}
	resolved, err := filepath.EvalSymlinks(literal)
	if err != nil {
		return pattern
	}
	return strings.Join(append([]string{strings.TrimSuffix(resolved, "/")}, segments[n:]...), "/")
}

// Key reads the private key. Errors never include the key contents.
func (i Identity) Key() (*rsa.PrivateKey, error) {
	if err := i.checkKeySource(); err != nil {
		return nil, err
	}
	if i.PrivateKeyPath != "" {
		pem, err := os.ReadFile(i.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read private key: %w", err)
		}
		key, err := ghapp.ParsePEM(pem)
		if err != nil {
			return nil, fmt.Errorf("private key %s: %w", i.PrivateKeyPath, err)
		}
		return key, nil
	}
	key, err := ghapp.ParsePEM([]byte(i.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("privateKey in config.json: %w", err)
	}
	return key, nil
}

func (c Config) validate() error {
	if c.Default == "" {
		return errors.New("default is empty")
	}
	if err := c.checkReference("default", c.Default); err != nil {
		return err
	}
	for _, name := range c.AppNames() {
		if err := ValidName(name); err != nil {
			return err
		}
		if err := c.Identities[name].validate(); err != nil {
			return fmt.Errorf("identity %q: %w", name, err)
		}
	}
	for i, r := range c.Rules {
		if err := c.validateRule(r); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	return nil
}

func (c Config) checkReference(where, name string) error {
	if _, ok := c.Identities[name]; !ok && name != User {
		return fmt.Errorf("%s refers to the unknown identity %q", where, name)
	}
	return nil
}

func (c Config) validateRule(r Rule) error {
	if err := c.checkReference("identity", r.Identity); err != nil {
		return err
	}
	if r.Path == "" && r.Remote == "" {
		return errors.New("a rule needs path or remote")
	}
	if r.Path != "" {
		if err := validatePathPattern(r.Path); err != nil {
			return fmt.Errorf("path: %w", err)
		}
	}
	if r.Remote != "" {
		if err := glob.Validate(r.Remote); err != nil {
			return fmt.Errorf("remote: %w", err)
		}
	}
	if r.Branch != "" {
		if err := glob.Validate(r.Branch); err != nil {
			return fmt.Errorf("branch: %w", err)
		}
	}
	return nil
}

func validatePathPattern(pattern string) error {
	if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "~/") {
		return fmt.Errorf("pattern %q must start with / or ~/", pattern)
	}
	return glob.Validate(pattern)
}

func (i Identity) validate() error {
	if i.ClientID == "" {
		return errors.New("clientId is empty")
	}
	return i.checkKeySource()
}

func (i Identity) checkKeySource() error {
	switch {
	case i.PrivateKey != "" && i.PrivateKeyPath != "":
		return errors.New("both privateKey and privateKeyPath are set; set only one of them")
	case i.PrivateKey == "" && i.PrivateKeyPath == "":
		return errors.New("either privateKey or privateKeyPath is required")
	}
	return nil
}
