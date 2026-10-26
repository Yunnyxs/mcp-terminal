package command

import (
	"strings"

	"terminal-mcp/internal/clierr"
)

type flagKV struct{ key, val string }

func splitArgs(args []string, valueFlags, boolFlags map[string]bool) (pos []string, flags []flagKV, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = args[i+1:]
			return
		}
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		k, v, hasV := strings.Cut(a[2:], "=")
		switch {
		case boolFlags[k]:
			if hasV {
				return nil, nil, nil, clierr.Usagef("--%s does not take a value", k)
			}
			flags = append(flags, flagKV{k, "true"})
		case valueFlags[k]:
			if !hasV {
				i++
				if i >= len(args) {
					return nil, nil, nil, clierr.Usagef("--%s needs a value", k)
				}
				v = args[i]
			}
			flags = append(flags, flagKV{k, v})
		default:
			return nil, nil, nil, clierr.Usagef("unknown option: --%s", k)
		}
	}
	return
}

func flagVal(flags []flagKV, key string) (string, bool) {
	val, found := "", false
	for _, f := range flags {
		if f.key == key {
			val, found = f.val, true
		}
	}
	return val, found
}

func flagAll(flags []flagKV, key string) []string {
	var out []string
	for _, f := range flags {
		if f.key == key {
			out = append(out, f.val)
		}
	}
	return out
}

func parseKV(items []string, what string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	m := map[string]string{}
	for _, it := range items {
		k, v, ok := strings.Cut(it, "=")
		if !ok || k == "" {
			return nil, clierr.Usagef("--%s must be NAME=VALUE, got: %q", what, it)
		}
		m[k] = v
	}
	return m, nil
}
