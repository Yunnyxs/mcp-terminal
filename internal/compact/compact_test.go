package compact

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestIsImportant(t *testing.T) {
	cases := map[string]bool{
		"error: undefined: Foo":                       true,
		"/usr/include/bits/error.h":                   false,
		"libgpg-error.so.0.42.1":                      false,
		"-rw-r--r-- 1 root root 12 Oct 1 error.h":     false,
		"build failed: 1 error, 1 warning":            true,
		"panic: runtime error: index out of range":    true,
		"ValueError: invalid literal":                 true,
		"2026-10-21T08:00:00Z ERROR req=7c83 timeout": true,
		"Build error.":                                true,
		"ok  github.com/x/pkg 0.4s":                   false,
		"WARNING: deprecated flag":                    true,
		"e2scrub_fail@.service":                       false,
	}
	for in, want := range cases {
		if got := isImportant(in); got != want {
			t.Errorf("isImportant(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestShape(t *testing.T) {
	same := [][2]string{
		{"req=dfbaaafa GET /x 1", "req=8b53c16b GET /x 2"},
		{"[  1/300] ok pkg-1", "[ 10/300] ok pkg-2"},
		{"GET /health 200 3ms", "GET /health 200 17ms"},
	}
	for _, c := range same {
		if shape(c[0]) != shape(c[1]) {
			t.Errorf("must be equal: %q vs %q", shape(c[0]), shape(c[1]))
		}
	}
	if shape("  indented 1") == shape("indented 1") {
		t.Error("leading indentation must be kept")
	}
	if got := shape("the word defaced here"); got != "the word defaced here" {
		t.Errorf("ordinary words must not become <hex>: %q", got)
	}
}

func steps(n int) []string {
	in := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		in = append(in, fmt.Sprintf("step %d of %d", i, n))
	}
	return in
}

func TestCollapseModes(t *testing.T) {
	out, lossy := collapse(analyze(steps(6)), true)
	if !lossy || len(out) != 3 || out[1].text != "[+4 similar]" || out[2].text != "step 6 of 6" {
		t.Errorf("similar=true: %+v", out)
	}
	out, lossy = collapse(analyze(steps(6)), false)
	if lossy || len(out) != 6 {
		t.Errorf("similar=false must not touch lines that are only similar: %+v", out)
	}
	out, lossy = collapse(analyze([]string{"x", "x", "x"}), false)
	if lossy || len(out) != 1 || out[0].text != "x (x3)" {
		t.Errorf("identical lines must always be merged without lossy: %+v", out)
	}
	if out, _ := collapse(analyze(steps(3)), true); len(out) != 3 {
		t.Errorf("3 similar lines are not enough to summarize: %+v", out)
	}
}

func TestBodyLosslessUnderBudget(t *testing.T) {
	raw := strings.Join(steps(6), "\n")
	if out, lossy := Body(raw, Options{Budget: 8000}); lossy || out != raw {
		t.Errorf("under the budget it must stay intact: lossy=%v out=%q", lossy, out)
	}
	if out, lossy := Body(raw, Options{Budget: 40}); !lossy || !strings.Contains(out, "[+4 similar]") {
		t.Errorf("over the budget it must be summarized: lossy=%v out=%q", lossy, out)
	}
}

func TestBodyCutLineIsLossy(t *testing.T) {
	out, lossy := Body(strings.Repeat("a", 1000), Options{Budget: 8000})
	if !lossy || !strings.Contains(out, "bytes...") {
		t.Errorf("truncated lines must be marked lossy: lossy=%v out=%q", lossy, out)
	}
}

func TestTrail(t *testing.T) {
	if trail("panic: x") != trailSevere || trail("error: x") != trailError || trail("warning: x") != contextLines {
		t.Error("wrong trail")
	}
}

func word(i int) string {
	var b []byte
	for ; i >= 0; i = i/26 - 1 {
		b = append(b, byte('g'+i%20))
	}
	return "item-" + string(b)
}

func TestFitKeepsPanicTrace(t *testing.T) {
	var lines []string
	for i := 0; i < 3000; i++ {
		if i == 1500 {
			lines = append(lines, "panic: runtime error: index out of range [5] with length 3", "",
				"goroutine 1 [running]:", "main.process(0xc000012345)", "\t/home/dev/app/main.go:88 +0x1d")
		}
		lines = append(lines, word(i)+"/error.h")
	}
	for _, budget := range []int{1500, 3000, 8000} {
		out, lossy := Body(strings.Join(lines, "\n"), Options{Budget: budget, Failed: true})
		for _, want := range []string{"panic: runtime error", "goroutine 1 [running]:", "main.go:88"} {
			if !strings.Contains(out, want) {
				t.Errorf("budget %d: %q is missing", budget, want)
			}
		}
		if !lossy || len(out) > budget {
			t.Errorf("budget %d: lossy=%v, length %d", budget, lossy, len(out))
		}
	}
}

func TestFatalSurvivesBudgets(t *testing.T) {
	var lines []string
	for i := 0; i < 1200; i++ {
		lines = append(lines, word(i)+"/error.h")
	}
	lines = append(lines, "fatal: disk full while writing /var/tmp/x")
	for i := 1200; i < 2400; i++ {
		lines = append(lines, word(i)+"/error.h")
	}
	lines = append(lines, "Done: 2400 files")
	raw := strings.Join(lines, "\n")

	for _, budget := range []int{200, 600, 1500, 3000} {
		for _, failed := range []bool{false, true} {
			out, _ := Body(raw, Options{Budget: budget, Failed: failed})
			if !strings.Contains(out, "fatal: disk full") || !strings.Contains(out, "Done: 2400 files") {
				t.Errorf("budget %d failed=%v: fatal or the last line is missing:\n%s", budget, failed, out)
			}
			if len(out) > budget {
				t.Errorf("budget %d failed=%v: length %d exceeds the budget", budget, failed, len(out))
			}
		}
	}
}

func TestBodyNeverExceedsBudget(t *testing.T) {
	var lines []string
	for i := 0; i < 1500; i++ {
		lines = append(lines, word(i)+" some words here")
		if i%700 == 0 {
			lines = append(lines, fmt.Sprintf("error: thing %c broke", 'a'+i%26))
		}
	}
	raw := strings.Join(lines, "\n")
	for _, budget := range []int{60, 200, 1000, 5000} {
		if out, _ := Body(raw, Options{Budget: budget}); len(out) > budget {
			t.Errorf("budget %d: length %d", budget, len(out))
		}
	}
}

func TestBodyKeepsValidUTF8(t *testing.T) {
	raw := strings.Repeat("日本語テキスト", 300) + "\n" + strings.Repeat("行", 4000)
	for _, budget := range []int{50, 333, 8000} {
		out, _ := Body(raw, Options{Budget: budget})
		if !utf8.ValidString(out) {
			t.Errorf("budget %d: result is not valid UTF-8", budget)
		}
	}
}

func TestCleanANSIAndProgress(t *testing.T) {
	got := clean("\x1b[31mred\x1b[0m\nprogress 10%\rprogress 100%\n\n\n\nend\n")
	want := []string{"red", "progress 100%", "", "end"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("clean = %q, want %q", got, want)
	}
}

func TestStatusAndJoin(t *testing.T) {
	cases := []struct {
		exit string
		dur  time.Duration
		want string
	}{
		{"0", time.Second, ""},
		{"0", 6 * time.Second, "(6s)"},
		{"2", 100 * time.Millisecond, "exit 2"},
		{"2", 12400 * time.Millisecond, "exit 2 (12s)"},
		{"signal: killed", 7500 * time.Millisecond, "exit signal: killed (8s)"},
	}
	for _, c := range cases {
		if got := Status(c.exit, c.dur); got != c.want {
			t.Errorf("Status(%q, %v) = %q, want %q", c.exit, c.dur, got, c.want)
		}
	}
	joins := [][3]string{{"", "", "ok"}, {"exit 2", "", "exit 2"}, {"", "x", "x"}, {"exit 2", "x", "exit 2\nx"}}
	for _, j := range joins {
		if got := Join(j[0], j[1]); got != j[2] {
			t.Errorf("Join(%q, %q) = %q, want %q", j[0], j[1], got, j[2])
		}
	}
}

func TestCollapseImportantFlood(t *testing.T) {
	flood := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		flood = append(flood, fmt.Sprintf("retry connection failed attempt %d", i))
	}
	out, lossy := collapse(analyze(flood), true)
	if !lossy || len(out) != 4 || out[0].text != flood[0] || out[1].text != flood[1] ||
		out[2].text != "[+17 similar]" || out[3].text != flood[19] {
		t.Errorf("a flood of important lines must be summarized: %+v", out)
	}
	if out, lossy := collapse(analyze(flood), false); lossy || len(out) != 20 {
		t.Errorf("exact-match mode must not touch important lines that are only similar: %d lines", len(out))
	}
	if out, _ := collapse(analyze(flood[:minRunImportant-1]), true); len(out) != minRunImportant-1 {
		t.Errorf("below minRunImportant, important lines must stay intact: %d lines", len(out))
	}

	distinct := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		distinct = append(distinct, "error: file "+string(rune('a'+i))+" missing")
	}
	if out, lossy := collapse(analyze(distinct), true); lossy || len(out) != 12 {
		t.Errorf("different errors must stay separate: lossy=%v %d lines", lossy, len(out))
	}
}

func TestBodyFloodFitsAndKeepsEnds(t *testing.T) {
	var b []string
	for i := 1; i <= 500; i++ {
		b = append(b, fmt.Sprintf("retry connection failed attempt %d", i))
	}
	out, lossy := Body(strings.Join(b, "\n"), Options{Budget: 2000})
	if !lossy || len(out) > 600 {
		t.Errorf("a flood must be summarized far below the budget: lossy=%v %d bytes", lossy, len(out))
	}
	for _, must := range []string{"attempt 1\n", "attempt 2\n", "[+497 similar]", "attempt 500"} {
		if !strings.Contains(out+"\n", must) {
			t.Errorf("%q is missing:\n%s", must, out)
		}
	}
}

func TestCloseSmallGaps(t *testing.T) {
	var in []string
	for i := 0; i < 12; i++ {
		in = append(in, "line "+word(i)+" xxxxxxxxxxxxxxxxxxxx")
	}
	ls := analyze(in)
	pattern := []bool{true, true, false, true, true, false, false, false, false, false, true, true}

	keep := append([]bool(nil), pattern...)
	before := len(render(ls, keep))
	if r := render(ls, keep); !strings.Contains(r, "[... 1 line omitted ...]") || !strings.Contains(r, "[... 5 lines omitted ...]") {
		t.Fatalf("wrong singular/plural marker:\n%s", r)
	}

	closeSmallGaps(ls, keep, before+1000)
	if !keep[2] {
		t.Error("a 1-line gap must be closed when the budget allows")
	}
	for i := 5; i <= 9; i++ {
		if keep[i] {
			t.Errorf("a 5-line gap must not be closed (line %d)", i)
		}
	}
	if got := len(render(ls, keep)); got > before+1000 {
		t.Errorf("exceeds the budget: %d", got)
	}

	tight := append([]bool(nil), pattern...)
	closeSmallGaps(ls, tight, before)
	if !reflect.DeepEqual(tight, pattern) {
		t.Errorf("an exact budget must not free up space: %v", tight)
	}
}

func TestFitClosesSmallGapsBeforeFilling(t *testing.T) {
	var in []string
	for i := 0; i < 200; i++ {
		if i == 50 || i == 54 || i == 58 || i == 62 {
			in = append(in, "warning: "+word(i)+" overheated "+strings.Repeat("y", 78))
		} else {
			in = append(in, "ln "+word(i)+" "+strings.Repeat("x", 85))
		}
	}

	const budget = 4000
	out, lossy := Body(strings.Join(in, "\n"), Options{Budget: budget})
	if !lossy || len(out) > budget {
		t.Fatalf("lossy=%v len=%d", lossy, len(out))
	}
	if strings.Contains(out, "1 line omitted") {
		t.Errorf("a 1-line gap must be closed:\n%s", out)
	}
	for _, i := range []int{52, 56, 60} {
		if !strings.Contains(out, in[i]) {
			t.Errorf("line %d (%q) must be kept", i, in[i])
		}
	}
}
