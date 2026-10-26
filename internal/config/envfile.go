package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

func FromEnvFile() (Settings, error) {
	path, err := EnvFilePath()
	if err != nil {
		return Settings{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Settings{}, fmt.Errorf("config not found: %s (run install.sh)", path)
	}
	if err != nil {
		return Settings{}, err
	}
	file := ParseEnv(string(data))
	return Load(func(key string) string {
		if v, ok := file[key]; ok {
			return v
		}
		if strings.HasPrefix(key, "TERMINAL_MCP_") {
			return ""
		}
		return os.Getenv(key)
	})
}

func ParseEnv(content string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		m[strings.TrimSpace(k)] = v
	}
	return m
}
