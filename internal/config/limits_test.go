package config

import (
	"testing"
	"time"
)

func TestDefaultLimits(t *testing.T) {
	if l := DefaultLimits(); l.Timeout != 120*time.Second || l.MaxOutput != 30000 {
		t.Errorf("DefaultLimits = %+v", l)
	}
}

func TestLenientLimitsFallsBackPerVariable(t *testing.T) {
	cases := []struct {
		name string
		kv   map[string]string
		want Limits
	}{
		{"empty", nil, Limits{120 * time.Second, 30000}},
		{"valid", map[string]string{"TERMINAL_MCP_TIMEOUT": "7", "TERMINAL_MCP_MAX_OUTPUT": "5000"}, Limits{7 * time.Second, 5000}},
		{"timeout invalid, output valid", map[string]string{"TERMINAL_MCP_TIMEOUT": "abc", "TERMINAL_MCP_MAX_OUTPUT": "5000"}, Limits{120 * time.Second, 5000}},
		{"output invalid, timeout valid", map[string]string{"TERMINAL_MCP_TIMEOUT": "7", "TERMINAL_MCP_MAX_OUTPUT": "-1"}, Limits{7 * time.Second, 30000}},
		{"out of range", map[string]string{"TERMINAL_MCP_TIMEOUT": "0", "TERMINAL_MCP_MAX_OUTPUT": "999"}, Limits{120 * time.Second, 30000}},
	}
	for _, c := range cases {
		if got := LenientLimits(env(c.kv)); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
