package config

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/store"
)

type Config struct {
	ClientID       string `json:"clientId"`
	PrivateKey     string `json:"privateKey,omitempty"`
	PrivateKeyPath string `json:"privateKeyPath,omitempty"`
	InstallationID int64  `json:"installationId"`
}

// Load reads the config.json at path. If the file does not exist, the error wraps fs.ErrNotExist.
// An unset installationId (0) is not an error. The caller tells the user to run botid init again.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read config file: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("cannot read %s: unexpected extra content after JSON", path)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save atomically writes c to path with mode 0600. The caller creates the parent directory.
func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode config as JSON: %w", err)
	}
	return store.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// Key reads the private key. Errors never include the key contents.
func (c Config) Key() (*rsa.PrivateKey, error) {
	if err := c.checkKeySource(); err != nil {
		return nil, err
	}
	if c.PrivateKeyPath != "" {
		pem, err := os.ReadFile(c.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read private key: %w", err)
		}
		key, err := ghapp.ParsePEM(pem)
		if err != nil {
			return nil, fmt.Errorf("private key %s: %w", c.PrivateKeyPath, err)
		}
		return key, nil
	}
	key, err := ghapp.ParsePEM([]byte(c.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("privateKey in config.json: %w", err)
	}
	return key, nil
}

func (c Config) validate() error {
	if c.ClientID == "" {
		return errors.New("clientId is empty")
	}
	return c.checkKeySource()
}

func (c Config) checkKeySource() error {
	switch {
	case c.PrivateKey != "" && c.PrivateKeyPath != "":
		return errors.New("both privateKey and privateKeyPath are set; set only one of them")
	case c.PrivateKey == "" && c.PrivateKeyPath == "":
		return errors.New("either privateKey or privateKeyPath is required")
	}
	return nil
}
