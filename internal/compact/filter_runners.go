package compact

import (
	"path"
	"regexp"
	"strings"
)

var (
	pytestFilter = Filter{
		Name: "pytest",
		Match: func(args []string) bool {
			t, _ := toolOf(args)
			return t == "pytest" || t == "py.test"
		},
		Apply: rulesFilter([]dropRule{

			boiler(`^=+ test session starts =+$`),
			boiler(`^(platform|cachedir:|rootdir:|configfile:|plugins:|testpaths:|inifile:)\s`),
			boiler(`^(collecting \.\.\.|collected \d+ items?)`),

			boiler(`^(\S+\.py\s+)?[.sSxX]+\s*(\[\s*\d+%\])?$`),

			lossyDrop(`^\S+::\S.*\bPASSED\b\s*(\[\s*\d+%\])?$`),
			lossyDrop(`^\[gw\d+\] \[\s*\d+%\] PASSED `),
		}),
	}

	cargoTestFilter = Filter{
		Name: "cargo test",
		Match: func(args []string) bool {
			if len(args) < 2 || path.Base(args[0]) != "cargo" {
				return false
			}
			rest := args[1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "+") {
				rest = rest[1:]
			}
			return len(rest) > 0 && (rest[0] == "test" || (rest[0] == "nextest" && len(rest) > 1 && rest[1] == "run"))
		},
		Apply: rulesFilter([]dropRule{
			boiler(`^\s*(Compiling|Downloading|Downloaded|Updating|Locking|Adding|Fresh|Blocking|Checking|Documenting|Packaging|Unpacking|Finished)\s`),
			boiler(`^running \d+ tests?$`),
			lossyDrop(`^test .+ \.\.\. ok$`),
			lossyDrop(`^\s+PASS \[`),
		}),
	}

	jsTestFilter = Filter{
		Name:  "js test",
		Match: matchJSTest,
		Apply: filterJSTest,
	}
)

func matchJSTest(args []string) bool {
	runners := []string{"jest", "vitest", "mocha"}
	t, rest := toolOf(args)
	switch t {
	case "jest", "vitest", "mocha":
		return true
	case "npx", "bunx", "pnpx":
		n, _ := firstNonFlag(rest, "-p", "--package", "-c", "--call")
		return isOneOf(n, runners...)
	case "npm", "yarn", "pnpm", "bun":
		sub, r := firstNonFlag(rest, "--prefix", "-C", "--cwd", "--filter", "-F", "-w", "--workspace")
		switch {
		case isOneOf(sub, "test", "t", "tst"), isOneOf(sub, runners...):
			return true
		case isOneOf(sub, "run", "run-script"):
			n, _ := firstNonFlag(r)
			return strings.HasPrefix(n, "test")
		case isOneOf(sub, "exec", "x"):
			n, _ := firstNonFlag(r)
			return isOneOf(n, runners...)
		}
	}
	return false
}

var (
	reJestPass = regexp.MustCompile(`^\s*PASS\s+\S`)
	reTestPass = regexp.MustCompile(`^\s*(?:[✓✔√]\s|\(pass\)\s)`)

	reJSConsole = regexp.MustCompile(`^\s*console\.(warn|error)\b`)
	reTestMark  = regexp.MustCompile(`^\s*[✓✔√✕✖×✗●❯]|^\s*\((pass|fail)\)`)
)

type jsEnt struct {
	text string
	keep bool
}

func filterJSTest(lines []string) ([]string, bool) {
	ents := make([]jsEnt, len(lines))
	lossy := false
	inSuite := false
	keepIndent := -1
	for i, l := range lines {
		top := l != "" && !isIndented(l)
		if top {
			inSuite = false
		}
		if keepIndent >= 0 && l != "" && indentOf(l) <= keepIndent {
			keepIndent = -1
		}
		e := jsEnt{text: l, keep: true}
		switch {
		case isImportant(l):
		case reJSConsole.MatchString(l):
			keepIndent = indentOf(l)
		case keepIndent >= 0:
		case i < 6 && strings.HasPrefix(l, "> "):
			e.keep = false
		case reJestPass.MatchString(l):
			e.keep, inSuite, lossy = false, true, true
		case inSuite && (l == "" || !top):
			e.keep = false
			if l != "" {
				lossy = true
			}
		case reTestPass.MatchString(l):
			e.keep, lossy = false, true
		}
		ents[i] = e
	}

	for i := len(ents) - 1; i >= 0; i-- {
		e := ents[i]
		if !e.keep || !isGroupHeader(e.text) {
			continue
		}
		ind := indentOf(e.text)
		kids, kept := 0, false
		for j := i + 1; j < len(ents); j++ {
			t := ents[j].text
			if strings.TrimSpace(t) == "" {
				continue
			}
			if indentOf(t) <= ind {
				break
			}
			kids++
			if ents[j].keep {
				kept = true
				break
			}
		}
		if kids > 0 && !kept {
			ents[i].keep, lossy = false, true
		}
	}
	out := make([]string, 0, len(lines))
	for _, e := range ents {
		if e.keep {
			out = append(out, e.text)
		}
	}
	return tidy(out), lossy
}

func isGroupHeader(l string) bool {
	return isIndented(l) && strings.TrimSpace(l) != "" && !reTestMark.MatchString(l) && !isImportant(l)
}
