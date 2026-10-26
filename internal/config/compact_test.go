package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func compactEnv(t *testing.T, extra map[string]string) Lookup {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{
		"TERMINAL_MCP_ROOT": dir,
		EnvDir:              filepath.Join(dir, "data"),
		EnvMCPs:             filepath.Join(dir, "mcps.json"),
	}
	for k, v := range extra {
		env[k] = v
	}
	return func(k string) string { return env[k] }
}

func TestCompactDefaults(t *testing.T) {
	s, err := Load(compactEnv(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Compact || s.LLMOutput != DefaultLLMOutput || DefaultLLMOutput != 8000 {
		t.Errorf("Compact = %v, LLMOutput = %d (default %d)", s.Compact, s.LLMOutput, DefaultLLMOutput)
	}

	if s.MaxOutput != DefaultMaxOutput {
		t.Errorf("MaxOutput = %d, want %d", s.MaxOutput, DefaultMaxOutput)
	}
}

func TestCompactFromEnv(t *testing.T) {
	for _, off := range []string{"0", "false", "FALSE", "off", "no", " 0 "} {
		s, err := Load(compactEnv(t, map[string]string{"TERMINAL_MCP_COMPACT": off}))
		if err != nil || s.Compact {
			t.Errorf("TERMINAL_MCP_COMPACT=%q: Compact = %v, err = %v", off, s.Compact, err)
		}
	}
	for _, on := range []string{"1", "true", "On", "yes"} {
		s, err := Load(compactEnv(t, map[string]string{"TERMINAL_MCP_COMPACT": on}))
		if err != nil || !s.Compact {
			t.Errorf("TERMINAL_MCP_COMPACT=%q: Compact = %v, err = %v", on, s.Compact, err)
		}
	}
	s, err := Load(compactEnv(t, map[string]string{"TERMINAL_MCP_LLM_OUTPUT": "1234"}))
	if err != nil || s.LLMOutput != 1234 {
		t.Errorf("LLMOutput = %d, err = %v", s.LLMOutput, err)
	}
}

func TestCompactRejectsBadValues(t *testing.T) {
	for _, c := range []struct{ key, val string }{
		{"TERMINAL_MCP_COMPACT", "maybe"},
		{"TERMINAL_MCP_LLM_OUTPUT", "abc"},
		{"TERMINAL_MCP_LLM_OUTPUT", "100"},
		{"TERMINAL_MCP_LLM_OUTPUT", "99999999"},
	} {
		_, err := Load(compactEnv(t, map[string]string{c.key: c.val}))
		if err == nil || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s=%q: err = %v, want an error naming the variable", c.key, c.val, err)
		}
	}
}
