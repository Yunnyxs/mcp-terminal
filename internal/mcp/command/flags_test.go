package command

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"terminal-mcp/internal/clierr"
)

func TestSplitArgs(t *testing.T) {
	vf := map[string]bool{"env": true, "timeout": true}
	bf := map[string]bool{"force": true}

	pos, flags, rest, err := splitArgs([]string{"name", "--env", "A=1", "--timeout=5", "tool", "--force", "--env=B=2", "--", "npx", "-y", "--flag"}, vf, bf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pos, []string{"name", "tool"}) {
		t.Errorf("pos = %v", pos)
	}
	if !reflect.DeepEqual(rest, []string{"npx", "-y", "--flag"}) {
		t.Errorf("rest = %v", rest)
	}
	if got := flagAll(flags, "env"); !reflect.DeepEqual(got, []string{"A=1", "B=2"}) {
		t.Errorf("env = %v", got)
	}
	if v, ok := flagVal(flags, "timeout"); !ok || v != "5" {
		t.Errorf("timeout = %q %v", v, ok)
	}
	if _, ok := flagVal(flags, "force"); !ok {
		t.Error("force is missing")
	}
	if _, ok := flagVal(flags, "absent"); ok {
		t.Error("a flag that was not given is reported as present")
	}

	for name, args := range map[string][]string{
		"unknown option":     {"--bogus"},
		"value is missing":   {"--env"},
		"bool given a value": {"--force=yes"},
	} {
		_, _, _, err := splitArgs(args, vf, bf)
		var ue clierr.Usage
		if !errors.As(err, &ue) {
			t.Errorf("%s: must be clierr.Usage, got %v", name, err)
		}
	}
}

func TestSplitArgsLastValueWins(t *testing.T) {
	_, flags, _, err := splitArgs([]string{"--timeout", "1", "--timeout", "9"}, map[string]bool{"timeout": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := flagVal(flags, "timeout"); v != "9" {
		t.Errorf("timeout = %q, want 9", v)
	}
}

func TestSplitArgsJSONPositionalNotTreatedAsFlag(t *testing.T) {
	pos, _, _, err := splitArgs([]string{"n", "t", `{"a":"--x"}`}, nil, nil)
	if err != nil || len(pos) != 3 {
		t.Fatalf("pos=%v err=%v", pos, err)
	}
}

func TestParseKV(t *testing.T) {
	m, err := parseKV([]string{"A=1", "B=x=y"}, "env")
	if err != nil || m["A"] != "1" || m["B"] != "x=y" {
		t.Errorf("%v %v", m, err)
	}
	if m, err := parseKV(nil, "env"); m != nil || err != nil {
		t.Errorf("empty = %v, %v", m, err)
	}
	for _, bad := range []string{"noequal", "=v"} {
		if _, err := parseKV([]string{bad}, "env"); err == nil || !strings.Contains(err.Error(), "NAME=VALUE") {
			t.Errorf("%q must be rejected: %v", bad, err)
		}
	}
}

func TestRedactURL(t *testing.T) {
	got := redactURL("https://user:pw@host.example/mcp?token=abc")
	if strings.Contains(got, "pw") || strings.Contains(got, "abc") || !strings.Contains(got, "host.example/mcp") {
		t.Errorf("got %q", got)
	}
	if got := redactURL("https://host.example/mcp"); got != "https://host.example/mcp" {
		t.Errorf("URL without a secret changed: %q", got)
	}
}

func TestHandles(t *testing.T) {
	for _, name := range []string{"load", "unload", "loaded", "tools", "call", "mcp", "mcp-help"} {
		if !Handles(name) {
			t.Errorf("%s must be handled", name)
		}
	}
	for _, name := range []string{"", "serve", "status", "mcphost", "Load"} {
		if Handles(name) {
			t.Errorf("%q must not be handled", name)
		}
	}
}
