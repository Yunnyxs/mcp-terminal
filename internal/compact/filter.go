package compact

import (
	"fmt"
	"regexp"
	"strings"
)

type Filter struct {
	Name string

	Match func(args []string) bool

	Apply func(lines []string) ([]string, bool)
}

const minFilterGain = 128

func byteSize(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	return n
}

var registry = []Filter{
	goTest,

	gitStatus, gitDiff, gitLog, gitTransfer, gitCommit, gitAdd,

	lsFilter, findFilter, treeFilter, grepFilter,

	pytestFilter, jsTestFilter, cargoTestFilter,

	pipFilter, nodeInstallFilter, aptFilter,

	dockerPsFilter, systemctlStatusFilter, journalctlFilter,
}

func lookupFilter(command string) *Filter {
	args, ok := simpleCommand(command)
	if !ok {
		return nil
	}
	args = stripSudo(args)
	if len(args) == 0 {
		return nil
	}
	for i := range registry {
		if registry[i].Match(args) {
			return &registry[i]
		}
	}
	return nil
}

var reEnvAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func simpleCommand(cmd string) ([]string, bool) {
	var (
		args   []string
		word   strings.Builder
		inWord bool
		redir  bool
		quote  rune
		cdDone bool
		rs     = []rune(cmd)
	)
	flush := func() bool {
		if !inWord {
			return true
		}
		w := word.String()
		word.Reset()
		inWord = false
		if redir {
			redir = false
			return w == "2>&1"
		}
		args = append(args, w)
		return true
	}
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if quote != 0 {
			switch {
			case c == quote:
				quote = 0
			case quote == '"' && (c == '$' || c == '`'):
				return nil, false
			case quote == '"' && c == '\\' && i+1 < len(rs):
				i++
				word.WriteRune(rs[i])
			default:
				word.WriteRune(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, inWord = c, true
		case ' ', '\t':
			if !flush() {
				return nil, false
			}
		case '&':
			switch {
			case i+1 < len(rs) && rs[i+1] == '&':
				if !flush() || cdDone || len(args) != 2 || args[0] != "cd" {
					return nil, false
				}
				cdDone, args = true, nil
				i++
			case inWord && i > 0 && rs[i-1] == '>':
				word.WriteRune(c)
			default:
				return nil, false
			}
		case '>', '<':
			redir, inWord = true, true
			word.WriteRune(c)
		case '\\':
			if i+1 >= len(rs) {
				return nil, false
			}
			i++
			word.WriteRune(rs[i])
			inWord = true
		case '|', ';', '\n', '(', ')', '`', '{', '}', '$':
			return nil, false
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	if quote != 0 || !flush() {
		return nil, false
	}
	for len(args) > 0 && reEnvAssign.MatchString(args[0]) {
		args = args[1:]
	}
	return args, true
}

var goTest = Filter{
	Name: "go test",
	Match: func(args []string) bool {
		if len(args) < 2 || args[0] != "go" || args[1] != "test" {
			return false
		}
		for _, a := range args[2:] {
			if a == "-args" {
				break
			}
			if a == "-json" {
				return false
			}
		}
		return true
	},
	Apply: filterGoTest,
}

var (
	reGoMarker = regexp.MustCompile(`^=== (RUN|PAUSE|CONT|NAME)(?:\s+(\S+))?`)

	reGoVerdict = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+)`)
	reGoNoTest  = regexp.MustCompile(`^\?\s+\S+\s+\[no test files\]$`)
)

func isOKLine(l string) bool {
	return strings.HasPrefix(l, "ok ") || strings.HasPrefix(l, "ok\t")
}

func isIndented(l string) bool {
	return strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

const (
	modeUnknown = iota
	modeAfterPass
	modeKeep
)

func isRunBoundary(l string) bool {
	return l == "FAIL" || strings.HasPrefix(l, "FAIL\t") || strings.HasPrefix(l, "FAIL ") || isOKLine(l) ||
		strings.HasPrefix(l, "panic:") || strings.HasPrefix(l, "fatal error:") || strings.HasPrefix(l, "exit status")
}

func filterGoTest(lines []string) ([]string, bool) {
	hasOK := false
	for _, l := range lines {
		if isOKLine(l) {
			hasOK = true
			break
		}
	}
	var (
		out     = make([]string, 0, len(lines))
		logs    = map[string][]string{}
		order   []string
		cur     string
		mode    = modeUnknown
		passed  int
		noTest  int
		dropped bool
	)

	buffer := func(name, l string) {
		if _, ok := logs[name]; !ok {
			order = append(order, name)
		}
		logs[name] = append(logs[name], l)
	}
	take := func(name string) []string {
		ls, ok := logs[name]
		if !ok {
			return nil
		}
		delete(logs, name)
		for i, n := range order {
			if n == name {
				order = append(order[:i], order[i+1:]...)
				break
			}
		}
		return ls
	}

	flush := func() {
		for _, n := range order {
			out = append(out, logs[n]...)
			delete(logs, n)
		}
		order = order[:0]
	}
	for _, l := range lines {
		if m := reGoMarker.FindStringSubmatch(l); m != nil {
			dropped, mode = true, modeUnknown
			if m[1] != "PAUSE" && m[2] != "" {
				cur = m[2]
			}
			continue
		}
		if v := reGoVerdict.FindStringSubmatch(l); v != nil {
			held := take(v[2])
			if v[1] == "PASS" {
				for _, h := range held {
					if isImportant(h) {
						out = append(out, h)
					} else {
						dropped = true
					}
				}
				dropped, mode = true, modeAfterPass
				if !isIndented(l) {
					passed++
				}
				continue
			}
			out = append(out, held...)
			out = append(out, l)
			mode = modeKeep
			continue
		}
		switch {
		case reGoNoTest.MatchString(l):
			flush()
			noTest++
			mode = modeUnknown
		case isIndented(l):
			switch {
			case mode == modeAfterPass && isImportant(l):
				out = append(out, l)
			case mode == modeAfterPass:
				dropped = true
			case mode == modeKeep:
				out = append(out, l)
			default:
				buffer(cur, l)
			}
		case l == "PASS" && hasOK:
			flush()
			mode = modeUnknown
		default:

			if isRunBoundary(l) {
				flush()
			} else {
				out = append(out, take(cur)...)
			}
			out = append(out, l)
			mode = modeKeep
		}
	}
	flush()
	if passed > 0 {
		out = append(out, "["+plural(passed, "test", "tests")+" passed]")
	}
	if noTest > 0 {
		out = append(out, "["+plural(noTest, "package", "packages")+" without test files]")
	}
	return out, dropped || passed > 0 || noTest > 0
}
