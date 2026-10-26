package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	EnvDir  = "TMCP_DIR"
	EnvMCPs = "TMCP_MCPS"
)

const (
	dataDirName   = ".mcp-terminal"
	configDirName = ".config/terminal-mcp"
	envFileName   = "config.env"
	mcpsFileName  = "mcps.json"
)

type Paths struct {
	Dir    string
	RunDir string
	LogDir string
	OutDir string
	MCPs   string
}

func PathsFrom(get Lookup) (Paths, error) {
	dir, mcps := get(EnvDir), get(EnvMCPs)
	if dir == "" || mcps == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("home directory not found, set %s and %s: %w", EnvDir, EnvMCPs, err)
		}
		if dir == "" {
			dir = filepath.Join(home, dataDirName)
		}
		if mcps == "" {
			mcps = filepath.Join(home, configDirName, mcpsFileName)
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, err
	}
	if mcps, err = filepath.Abs(mcps); err != nil {
		return Paths{}, err
	}
	return Paths{
		Dir:    dir,
		RunDir: filepath.Join(dir, "run"),
		LogDir: filepath.Join(dir, "mcp"),
		OutDir: filepath.Join(dir, "mcp", "out"),
		MCPs:   mcps,
	}, nil
}

func PathsFromEnv() (Paths, error) { return PathsFrom(os.Getenv) }

func EnvFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDirName, envFileName), nil
}
