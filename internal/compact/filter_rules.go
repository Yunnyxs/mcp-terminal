package compact

import (
	"path"
	"regexp"
	"strings"
)

type dropRule struct {
	re        *regexp.Regexp
	one, many string
	lossy     bool
}

func boiler(expr string) dropRule { return dropRule{re: regexp.MustCompile(expr)} }

func lossyDrop(expr string) dropRule { return dropRule{re: regexp.MustCompile(expr), lossy: true} }

func countDrop(expr, one, many string) dropRule {
	return dropRule{re: regexp.MustCompile(expr), one: one, many: many}
}

func dropFilter(lines []string, rules []dropRule) ([]string, bool) {
	counts := make([]int, len(rules))
	out := make([]string, 0, len(lines))
	lossy := false
	for _, l := range lines {
		hit := -1
		if !isImportant(l) {
			for i := range rules {
				if rules[i].re.MatchString(l) {
					hit = i
					break
				}
			}
		}
		if hit < 0 {
			out = append(out, l)
			continue
		}
		counts[hit]++
		if rules[hit].lossy || rules[hit].many != "" {
			lossy = true
		}
	}
	for i, r := range rules {
		if counts[i] > 0 && r.many != "" {
			out = append(out, "["+plural(counts[i], r.one, r.many)+"]")
		}
	}
	return tidy(out), lossy
}

func tidy(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func isOneOf(s string, opts ...string) bool {
	for _, o := range opts {
		if s == o {
			return true
		}
	}
	return false
}

func hasAny(args []string, names ...string) bool {
	for _, a := range args {
		if isOneOf(a, names...) {
			return true
		}
	}
	return false
}

func firstNonFlag(args []string, valueFlags ...string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			continue
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if isOneOf(a, valueFlags...) {
				i++
			}
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

var rePython = regexp.MustCompile(`^(python[0-9.]*|py)$`)

func toolOf(args []string) (string, []string) {
	if len(args) >= 3 && isOneOf(args[0], "uv", "poetry", "pipenv", "pdm", "rye", "hatch") && args[1] == "run" {
		args = args[2:]
	}
	if len(args) == 0 {
		return "", nil
	}
	if len(args) >= 3 && rePython.MatchString(path.Base(args[0])) && args[1] == "-m" {
		return args[2], args[3:]
	}
	return path.Base(args[0]), args[1:]
}

func stripSudo(args []string) []string {
	if len(args) == 0 || args[0] != "sudo" {
		return args
	}
	i := 1
	for i < len(args) && isOneOf(args[i], "-n", "-E", "-H", "-S", "--") {
		i++
	}
	return args[i:]
}

func indentOf(l string) int {
	n := 0
	for n < len(l) && (l[n] == ' ' || l[n] == '\t') {
		n++
	}
	return n
}
