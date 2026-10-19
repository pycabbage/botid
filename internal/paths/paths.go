package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir      string
	ConfigFile     string
	PrivateKeyFile string
	DataDir        string
	GHConfigDir    string
	HostsFile      string
	GitConfigDir   string
	GitConfigFile  string
}

func New(home string) Paths {
	configDir := filepath.Join(home, ".config", "botid")
	dataDir := filepath.Join(home, ".local", "share", "botid")
	ghConfigDir := filepath.Join(dataDir, "gh")
	gitConfigDir := filepath.Join(dataDir, "git")
	return Paths{
		ConfigDir:      configDir,
		ConfigFile:     filepath.Join(configDir, "config.json"),
		PrivateKeyFile: filepath.Join(configDir, "private-key.pem"),
		DataDir:        dataDir,
		GHConfigDir:    ghConfigDir,
		HostsFile:      filepath.Join(ghConfigDir, "hosts.yml"),
		GitConfigDir:   gitConfigDir,
		GitConfigFile:  filepath.Join(gitConfigDir, "config"),
	}
}

func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory is not an absolute path: %q", home)
	}
	return home, nil
}
