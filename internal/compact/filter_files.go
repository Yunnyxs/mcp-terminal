package compact

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	lsFilter = Filter{
		Name:  "ls",
		Match: func(args []string) bool { return args[0] == "ls" },
		Apply: filterLs,
	}
	findFilter = Filter{
		Name: "find",
		Match: func(args []string) bool {
			return args[0] == "find" && !hasAny(args, "-exec", "-execdir", "-ok", "-okdir", "-printf", "-fprintf",
				"-print0", "-ls", "-fls", "-fprint", "-fprint0", "-delete")
		},
		Apply: filterFind,
	}
	treeFilter = Filter{
		Name: "tree",
		Match: func(args []string) bool {
			return args[0] == "tree" && !hasAny(args, "-J", "--json", "-X", "--xml", "-H", "--html")
		},
		Apply: filterTree,
	}
	grepFilter = Filter{
		Name: "grep/rg",
		Match: func(args []string) bool {
			return isOneOf(args[0], "grep", "egrep", "fgrep", "rg") && grepGroupable(args)
		},
		Apply: filterGrep,
	}
)

var reLsLong = regexp.MustCompile(`^([-dl])([-rwxsStTl?]{9})[.+@]?\s+\d+\s+(\S+)\s+(\S+)\s+(\d+(?:\.\d+)?[KMGTPE]?)\s+` +
	`(?:[A-Z][a-z]{2}\s+\d{1,2}\s+(?:\d{1,2}:\d{2}|\d{4})|` +
	`\d{4}-\d{2}-\d{2}(?:[ T]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:\s*[+-]\d{4})?)?)\s(.+)$`)

var reLsTotal = regexp.MustCompile(`^total \d+$`)

func filterLs(lines []string) ([]string, bool) {
	type parsed struct{ m []string }
	owners := map[string]int{}
	ps := make([]*parsed, len(lines))
	matched := 0
	for i, l := range lines {
		if m := reLsLong.FindStringSubmatch(l); m != nil {
			ps[i] = &parsed{m}
			owners[m[3]+":"+m[4]]++
			matched++
		}
	}
	if matched == 0 {
		return lines, false
	}
	major, best := "", 0
	for o, n := range owners {
		if n > best || (n == best && o < major) {
			major, best = o, n
		}
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, "[owner "+major+"]")
	for i, l := range lines {
		if reLsTotal.MatchString(l) {
			continue
		}
		p := ps[i]
		if p == nil {
			out = append(out, l)
			continue
		}
		kind, perm, owner, group, sz, name := p.m[1], p.m[2], p.m[3], p.m[4], p.m[5], p.m[6]
		if name == "." || name == ".." {
			continue
		}
		var b strings.Builder
		b.WriteString(name)
		switch kind {
		case "d":
			b.WriteString("/")
		case "-":
			if perm[2] == 'x' || perm[2] == 's' {
				b.WriteString("*")
			}
			b.WriteString(" (" + lsSize(sz) + ")")
		}
		if kind != "l" {
			if mode := lsMode(perm); !isOneOf(mode, "644", "664", "755", "775") {
				b.WriteString(" [" + mode + "]")
			}
		}
		if o := owner + ":" + group; o != major {
			b.WriteString(" (" + o + ")")
		}
		out = append(out, b.String())
	}
	return out, true
}

func lsSize(s string) string {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	return humanBytes(n)
}

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	const units = "KMGTPE"
	v, i := float64(n), -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 10 {
		return fmt.Sprintf("%.0f%c", v, units[i])
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + string(units[i])
}

func lsMode(perm string) string {
	d := make([]byte, 3)
	for i := 0; i < 3; i++ {
		v := 0
		if perm[i*3] == 'r' {
			v += 4
		}
		if perm[i*3+1] == 'w' {
			v += 2
		}
		if isOneOf(string(perm[i*3+2]), "x", "s", "t") {
			v++
		}
		d[i] = byte('0' + v)
	}
	return string(d)
}

const findMaxLine = 200

func filterFind(lines []string) ([]string, bool) {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		dir, _, ok := splitFindPath(lines[i])
		if !ok {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) {
			d, _, ok2 := splitFindPath(lines[j])
			if !ok2 || d != dir {
				break
			}
			j++
		}
		if j-i < 2 {
			out = append(out, lines[i])
			i++
			continue
		}
		cur := dir + ":"
		first := true
		for _, l := range lines[i:j] {
			_, base, _ := splitFindPath(l)
			add := " " + base
			if !first {
				add = ", " + base
			}
			if !first && len(cur)+len(add) > findMaxLine {
				out = append(out, cur)
				cur, first = dir+":", true
				add = " " + base
			}
			cur += add
			first = false
		}
		out = append(out, cur)
		i = j
	}
	return out, false
}

func splitFindPath(l string) (dir, base string, ok bool) {
	if l == "" || strings.HasPrefix(l, "find: ") || strings.HasPrefix(l, "find:\t") {
		return "", "", false
	}
	i := strings.LastIndexByte(l, '/')
	if i < 0 || i == len(l)-1 {
		return "", "", false
	}
	return l[:i+1], l[i+1:], true
}

func treeDepth(l string) (depth int, name string, ok bool) {
	rs := []rune(l)
	i := 0
	for i+4 <= len(rs) {
		u := strings.ReplaceAll(string(rs[i:i+4]), "\u00a0", " ")
		switch u {
		case "│   ", "    ", "|   ":
			i += 4
			depth++
			continue
		case "├── ", "└── ", "|-- ", "`-- ":
			return depth, string(rs[i+4:]), true
		}
		break
	}
	return 0, l, false
}

func filterTree(lines []string) ([]string, bool) {
	out := make([]string, len(lines))
	for i, l := range lines {
		if d, name, ok := treeDepth(l); ok {
			out[i] = strings.Repeat("  ", d+1) + name
		} else {
			out[i] = l
		}
	}
	return out, false
}

var (
	reGrepNum   = regexp.MustCompile(`^([^\s:]+):(\d+):(.*)$`)
	reGrepPlain = regexp.MustCompile(`^([^\s:]+):(.*)$`)
)

func grepGroupable(args []string) bool {
	for _, a := range args[1:] {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "--") {
			name := a
			if i := strings.IndexByte(a, '='); i >= 0 {
				name = a[:i]
			}
			if isOneOf(name, "--count", "--files-with-matches", "--files-without-match", "--files", "--null", "--null-data",
				"--json", "--vimgrep", "--column", "--context", "--after-context", "--before-context", "--heading", "--quiet", "--silent") {
				return false
			}
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if strings.ContainsAny(a[1:], "clLqZzABC") {
				return false
			}
		}
	}
	return true
}

func filterGrep(lines []string) ([]string, bool) {
	numCnt, plainCnt := 0, 0
	for _, l := range lines {
		if m := reGrepNum.FindStringSubmatch(l); m != nil && looksLikePath(m[1]) {
			numCnt++
		} else if m := reGrepPlain.FindStringSubmatch(l); m != nil && looksLikePath(m[1]) {
			plainCnt++
		}
	}
	if numCnt+plainCnt == 0 {
		return lines, false
	}
	numbered := numCnt*5 >= (numCnt+plainCnt)*4
	parse := func(l string) (path, rest string, ok bool) {
		if numbered {
			if m := reGrepNum.FindStringSubmatch(l); m != nil && looksLikePath(m[1]) {
				return m[1], m[2] + ": " + m[3], true
			}
			return "", "", false
		}
		if m := reGrepPlain.FindStringSubmatch(l); m != nil && looksLikePath(m[1]) {
			return m[1], m[2], true
		}
		return "", "", false
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		p, _, ok := parse(lines[i])
		if !ok {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) {
			q, _, ok2 := parse(lines[j])
			if !ok2 || q != p {
				break
			}
			j++
		}
		if j-i < 2 {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, p)
		for _, l := range lines[i:j] {
			_, rest, _ := parse(l)
			out = append(out, "  "+rest)
		}
		i = j
	}
	return out, false
}
