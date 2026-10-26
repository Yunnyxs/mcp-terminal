package compact

import (
	"path"
	"regexp"
	"strings"
)

var (
	dockerPsFilter = Filter{
		Name:  "docker ps",
		Match: matchDockerPs,
		Apply: filterDockerPs,
	}
	systemctlStatusFilter = Filter{
		Name: "systemctl status",
		Match: func(args []string) bool {
			if args[0] != "systemctl" {
				return false
			}
			sub, _ := firstNonFlag(args[1:], "-n", "--lines", "-H", "--host", "-M", "--machine", "-o", "--output", "-p", "--property", "-t", "--type", "--state")
			return sub == "status"
		},
		Apply: rulesFilter([]dropRule{

			lossyDrop(`^\s+(Docs|Tasks|Memory|CPU|IO|IP|Invocation|TriggeredBy|CGroup):`),

			lossyDrop(`^\s+Process:.*\(code=exited, status=0/SUCCESS\)$`),
			lossyDrop(`^\s+[├└│]`),
			boiler(`^Hint: Some lines were ellipsized`),
			boiler(`^lines \d+-\d+/\d+`),
		}),
	}
	journalctlFilter = Filter{
		Name:  "journalctl",
		Match: func(args []string) bool { return args[0] == "journalctl" },
		Apply: filterJournal,
	}
)

func matchDockerPs(args []string) bool {
	if len(args) < 2 || path.Base(args[0]) != "docker" {
		return false
	}
	rest := args[1:]
	switch {
	case rest[0] == "compose" && len(rest) > 1 && rest[1] == "ps":
		rest = rest[2:]
	case rest[0] == "container" && len(rest) > 1 && isOneOf(rest[1], "ls", "list", "ps"):
		rest = rest[2:]
	case rest[0] == "ps":
		rest = rest[1:]
	default:
		return false
	}
	for _, a := range rest {
		switch {
		case a == "--quiet" || strings.HasPrefix(a, "--format"):
			return false
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a, 'q'):
			return false
		}
	}
	return true
}

var reTableCell = regexp.MustCompile(`\S+(?: \S+)*`)

func filterDockerPs(lines []string) ([]string, bool) {
	if len(lines) < 2 {
		return lines, false
	}
	hdr := lines[0]
	if !(strings.HasPrefix(hdr, "CONTAINER ID") || strings.HasPrefix(hdr, "NAME")) ||
		!strings.Contains(hdr, "IMAGE") || !strings.Contains(hdr, "STATUS") {
		return lines, false
	}
	type col struct {
		name  string
		start int
	}
	var cols []col
	for _, m := range reTableCell.FindAllStringIndex(hdr, -1) {
		cols = append(cols, col{hdr[m[0]:m[1]], m[0]})
	}

	want := []string{"NAMES", "NAME", "SERVICE", "IMAGE", "STATUS", "PORTS"}
	var idx []int
	for _, w := range want {
		for i, c := range cols {
			if c.name == w {
				idx = append(idx, i)
			}
		}
	}
	if len(idx) < 3 {
		return lines, false
	}
	cell := func(rs []rune, i int) string {
		from := cols[i].start
		if from >= len(rs) {
			return ""
		}
		to := len(rs)
		if i+1 < len(cols) && cols[i+1].start < to {
			to = cols[i+1].start
		}
		return strings.TrimSpace(string(rs[from:to]))
	}
	row := func(get func(i int) string) string {
		parts := make([]string, 0, len(idx))
		for _, i := range idx {
			v := get(i)
			if cols[i].name == "PORTS" {
				v = dedupePorts(v)
			}
			if v == "" {
				v = "-"
			}
			parts = append(parts, v)
		}
		return strings.Join(parts, "  ")
	}
	out := make([]string, 0, len(lines))
	out = append(out, row(func(i int) string { return cols[i].name }))
	for _, l := range lines[1:] {
		rs := []rune(l)
		out = append(out, row(func(i int) string { return cell(rs, i) }))
	}
	return out, true
}

func dedupePorts(s string) string {
	if s == "" {
		return s
	}
	var keep []string
	for _, p := range strings.Split(s, ", ") {
		if strings.HasPrefix(p, ":::") || strings.HasPrefix(p, "[::]:") {
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == 0 {
		return s
	}
	return strings.Join(keep, ", ")
}

var (
	reJournalSyslog = regexp.MustCompile(`^([A-Z][a-z]{2} [ \d]\d \d{2}:\d{2}:\d{2}) (\S+) (.*)$`)
	reJournalISO    = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\S+) (\S+) (.*)$`)
	reJournalHeader = regexp.MustCompile(`^-- (Logs begin at|Journal begins at)`)
)

func filterJournal(lines []string) ([]string, bool) {
	host := ""
	n := 0
	for _, l := range lines {
		m := reJournalSyslog.FindStringSubmatch(l)
		if m == nil {
			m = reJournalISO.FindStringSubmatch(l)
		}
		if m == nil {
			continue
		}
		if host == "" {
			host = m[2]
		} else if host != m[2] {
			return lines, false
		}
		n++
	}
	if n == 0 {
		return lines, false
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, "[host: "+host+"]")
	for _, l := range lines {
		if reJournalHeader.MatchString(l) {
			continue
		}
		if m := reJournalSyslog.FindStringSubmatch(l); m != nil {
			out = append(out, m[1]+" "+m[3])
		} else if m := reJournalISO.FindStringSubmatch(l); m != nil {
			out = append(out, m[1]+" "+m[3])
		} else {
			out = append(out, l)
		}
	}
	return out, false
}
