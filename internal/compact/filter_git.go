package compact

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

func gitSub(args []string) (string, []string) {
	if len(args) == 0 || args[0] != "git" {
		return "", nil
	}
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case isOneOf(a, "-C", "-c", "--git-dir", "--work-tree", "--namespace"):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

func gitFilter(name string, subs []string, apply func([]string) ([]string, bool)) Filter {
	return Filter{
		Name: name,
		Match: func(args []string) bool {
			s, _ := gitSub(args)
			return s != "" && slices.Contains(subs, s)
		},
		Apply: apply,
	}
}

func rulesFilter(rules []dropRule) func([]string) ([]string, bool) {
	return func(ls []string) ([]string, bool) { return dropFilter(ls, rules) }
}

var (
	gitStatus = gitFilter("git status", []string{"status"}, rulesFilter([]dropRule{
		boiler(`^\s*\((use "git |commit or discard the untracked|fix conflicts and run "git commit"|all conflicts fixed: run "git commit")`),
		boiler(`^no changes added to commit \(`),
	}))

	gitDiff = gitFilter("git diff", []string{"diff", "show"}, rulesFilter([]dropRule{
		boiler(`^index [0-9a-f,]+\.\.[0-9a-f]+(?: \d+)?$`),
	}))

	gitTransfer = gitFilter("git push/pull", []string{"push", "pull", "fetch", "clone"}, rulesFilter([]dropRule{
		boiler(`^(remote: )?(Enumerating objects|Counting objects|Compressing objects|Writing objects|Receiving objects|Resolving deltas|Unpacking objects|Updating files|Total \d+ )`),
		boiler(`^Delta compression using `),
		boiler(`^remote:\s*$`),
	}))

	gitCommit = gitFilter("git commit", []string{"commit"}, rulesFilter([]dropRule{
		countDrop(`^\s*create mode \d+ `, "file created", "files created"),
		countDrop(`^\s*delete mode \d+ `, "file deleted", "files deleted"),
	}))

	gitAdd = gitFilter("git add", []string{"add"}, rulesFilter([]dropRule{
		countDrop(`^add '.*'$`, "file added", "files added"),
		countDrop(`^remove '.*'$`, "file removed", "files removed"),
	}))

	gitLog = gitFilter("git log", []string{"log"}, filterGitLog)
)

var (
	reGitCommit = regexp.MustCompile(`^commit ([0-9a-f]{7,64})(?: \((.*)\))?(?: \(from [0-9a-f]+\))?$`)
	reGitAuthor = regexp.MustCompile(`^Author:\s+(.*?)(?:\s*<[^>]*>)?\s*$`)
)

func filterGitLog(lines []string) ([]string, bool) {
	if len(lines) == 0 || !reGitCommit.MatchString(lines[0]) {
		return lines, false
	}
	type commit struct{ hash, refs, author, date, subject string }
	var cs []commit
	cur := -1
	for _, l := range lines {
		if m := reGitCommit.FindStringSubmatch(l); m != nil {
			hash := m[1]
			if len(hash) > 7 {
				hash = hash[:7]
			}
			cs = append(cs, commit{hash: hash, refs: m[2]})
			cur = len(cs) - 1
			continue
		}
		switch {
		case cur < 0:
			return lines, false
		case strings.HasPrefix(l, "Author:"):
			if m := reGitAuthor.FindStringSubmatch(l); m != nil {
				cs[cur].author = m[1]
			}
		case strings.HasPrefix(l, "Date:"):
			cs[cur].date = gitDate(strings.TrimSpace(strings.TrimPrefix(l, "Date:")))
		case strings.HasPrefix(l, "Merge:"), l == "":
		case strings.HasPrefix(l, "    "):
			if cs[cur].subject == "" {
				cs[cur].subject = strings.TrimSpace(l)
			}
		default:
			return lines, false
		}
	}
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		subj := c.subject
		if subj == "" {
			subj = "(no message)"
		}
		s := c.hash
		if c.refs != "" {
			s += " (" + c.refs + ")"
		}
		s += " " + subj
		if who := strings.TrimSpace(c.author + ", " + c.date); who != "," && who != "" {
			s += " (" + strings.Trim(who, ", ") + ")"
		}
		out = append(out, s)
	}
	return out, true
}

func gitDate(s string) string {
	if t, err := time.Parse("Mon Jan 2 15:04:05 2006 -0700", s); err == nil {
		return t.Format("2006-01-02")
	}
	return s
}
