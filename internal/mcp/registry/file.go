package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"terminal-mcp/internal/fsutil"
)

func Load(path string) (Config, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("permissions on %s are too open (%04o): run chmod 600 %s", path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Config{}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: trailing data after the JSON object", path)
	}
	for name, e := range cfg {
		if err := ValidName(name); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := e.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %q: %w", path, name, err)
		}
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}
