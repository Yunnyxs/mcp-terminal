package compact

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	longLine     = 300
	longHead     = 200
	longTail     = 60
	contextLines = 1
	trailSevere  = 10
	trailError   = 3
)

var (
	reANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

	reImportant = regexp.MustCompile(`(?i)error|fail|panic|fatal|warn|exception|traceback|denied`)

	reWhole  = regexp.MustCompile(`(?i)\b(fatal|panic|traceback|exception|error|fail|failed|failure|warn|warning|denied)\b`)
	reSevere = regexp.MustCompile(`(?i)\b(fatal|panic|traceback|exception)\b`)
	reError  = regexp.MustCompile(`(?i)\b(error|fail|failed|failure)\b`)

	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	reUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reHexWord   = regexp.MustCompile(`\b(0x[0-9a-fA-F]+|[0-9a-fA-F]{7,64})\b`)
	reNumber    = regexp.MustCompile(`\d+(\.\d+)?`)

	reFileExt = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)
	reSpaces  = regexp.MustCompile(`[ \t]+`)
)

type line struct {
	text      string
	important bool
	key       string
	shape     string
	score     int
	cut       bool
}

func clean(raw string) []string {
	raw = reANSI.ReplaceAllString(raw, "")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	var out []string
	prevBlank := false
	for _, ln := range strings.Split(raw, "\n") {

		if strings.Contains(ln, "\r") {
			segs := strings.Split(ln, "\r")
			ln = ""
			for i := len(segs) - 1; i >= 0; i-- {
				if strings.TrimSpace(segs[i]) != "" {
					ln = segs[i]
					break
				}
			}
		}
		ln = strings.TrimRight(ln, " \t")
		if ln == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out = append(out, ln)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func analyze(lines []string) []line {
	out := make([]line, 0, len(lines))
	for _, l := range lines {
		imp := isImportant(l)
		sh := shape(l)
		key := l
		if !imp {
			key = sh
		} else {
			sh += "\x00!"
		}
		sc := 0
		if imp {
			sc = score(l)
		}
		txt := shorten(l)
		out = append(out, line{text: txt, important: imp, key: key, shape: sh, score: sc, cut: txt != l})
	}
	return out
}

func isImportant(l string) bool {
	for _, m := range reImportant.FindAllStringIndex(l, -1) {
		a, b := m[0], m[1]
		for a > 0 && l[a-1] != ' ' && l[a-1] != '\t' {
			a--
		}
		for b < len(l) && l[b] != ' ' && l[b] != '\t' {
			b++
		}
		if !looksLikePath(l[a:b]) {
			return true
		}
	}
	return false
}

func looksLikePath(tok string) bool {
	return strings.Contains(tok, "/") || reFileExt.MatchString(tok)
}

func trail(s string) int {
	switch {
	case reSevere.MatchString(s):
		return trailSevere
	case reError.MatchString(s):
		return trailError
	}
	return contextLines
}

func score(s string) int {
	n := 0
	if reWhole.MatchString(s) {
		n += 2
	}
	if strings.ContainsAny(s, " \t") {
		n += 2
	}
	if reSevere.MatchString(s) {
		n += 2
	} else if reError.MatchString(s) {
		n++
	}
	return n
}

func squeeze(s string) string {
	body := strings.TrimLeft(s, " \t")
	return s[:len(s)-len(body)] + reSpaces.ReplaceAllString(body, " ")
}

func shape(s string) string {
	s = reTimestamp.ReplaceAllString(s, "<ts>")
	s = reUUID.ReplaceAllString(s, "<id>")
	s = reHexWord.ReplaceAllStringFunc(s, func(m string) string {

		if strings.HasPrefix(m, "0x") || len(m) >= 8 || strings.ContainsAny(m, "0123456789") {
			return "<hex>"
		}
		return m
	})
	return squeeze(reNumber.ReplaceAllString(s, "<n>"))
}

func shorten(s string) string {
	if len(s) <= longLine {
		return s
	}
	h, t := prefix(s, longHead), suffix(s, longTail)
	return fmt.Sprintf("%s[...%d bytes...]%s", h, len(s)-len(h)-len(t), t)
}

func prefix(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func suffix(s string, n int) string {
	if n >= len(s) {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}
