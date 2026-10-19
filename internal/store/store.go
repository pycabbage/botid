package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := fillTemp(f, data, perm); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func fillTemp(f *os.File, data []byte, perm os.FileMode) error {
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Chmod(perm); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	// MkdirAll does not fix the permissions of an existing directory, and the umask applies to newly created ones.
	return os.Chmod(path, 0o700)
}

const yamlIndicators = "!&*-?:,[]{}#|>'%@`"

func HostsYAML(slug, token string) ([]byte, error) {
	if err := checkYAMLValue("slug", slug); err != nil {
		return nil, err
	}
	if err := checkYAMLValue("token", token); err != nil {
		return nil, err
	}
	if strings.ContainsRune(yamlIndicators, []rune(token)[0]) {
		return nil, errors.New("token must not start with a YAML indicator character")
	}
	return fmt.Appendf(nil, `github.com:
    users:
        "%[1]s[bot]":
            oauth_token: %[2]s
    git_protocol: https
    user: "%[1]s[bot]"
    oauth_token: %[2]s
`, slug, token), nil
}

func checkYAMLValue(name, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty", name)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s contains invalid UTF-8", name)
	}
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), unicode.IsControl(r):
			return fmt.Errorf("%s must not contain whitespace or control characters", name)
		case r == '"', r == '\\':
			return fmt.Errorf("%s must not contain %q", name, r)
		}
	}
	return nil
}

func DotEnv(ghConfigDir, gitConfigGlobal string) (string, error) {
	if err := checkEnvPath("GH_CONFIG_DIR", ghConfigDir); err != nil {
		return "", err
	}
	if err := checkEnvPath("GIT_CONFIG_GLOBAL", gitConfigGlobal); err != nil {
		return "", err
	}
	return "GH_CONFIG_DIR='" + ghConfigDir + "'\n" +
		"GIT_CONFIG_GLOBAL='" + gitConfigGlobal + "'\n", nil
}

func checkEnvPath(name, p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s is not an absolute path: %q", name, p)
	}
	if strings.ContainsAny(p, "'\n\r") {
		return fmt.Errorf("%s must not contain a single quote or newline: %q", name, p)
	}
	return nil
}
