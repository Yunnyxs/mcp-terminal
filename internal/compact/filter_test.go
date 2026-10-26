package compact

import (
	"reflect"
	"strings"
	"testing"
)

func TestSimpleCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"go test ./...", []string{"go", "test", "./..."}},
		{"  go   test -v ./internal/compact  ", []string{"go", "test", "-v", "./internal/compact"}},
		{"go test -v ./... 2>&1", []string{"go", "test", "-v", "./..."}},
		{"cd /tmp/x && go test ./...", []string{"go", "test", "./..."}},
		{"GOCACHE=/tmp/c CGO_ENABLED=0 go test", []string{"go", "test"}},
		{`go test -run 'TestA|TestB' ./...`, []string{"go", "test", "-run", "TestA|TestB", "./..."}},
		{`go test -run "Test A" ./...`, []string{"go", "test", "-run", "Test A", "./..."}},
		{"go test ./... | tail -20", nil},
		{"go test ./... && echo done", nil},
		{"cd a && cd b && go test", nil},
		{"echo hi && go test", nil},
		{"go test ./...; echo $?", nil},
		{"go test ./... > out.txt", nil},
		{"go test ./... 2>/dev/null", nil},
		{"go test $(pwd)/...", nil},
		{`go test -run "$X"`, nil},
		{"go test ./... &", nil},
		{"go test 'unterminated", nil},
		{"(cd x && go test)", nil},
		{"", []string{}},
	}
	for _, c := range cases {
		got, ok := simpleCommand(c.in)
		if c.want == nil {
			if ok {
				t.Errorf("simpleCommand(%q) = %q, should have been rejected", c.in, got)
			}
			continue
		}
		if !ok || len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("simpleCommand(%q) = %q ok=%v, want %q", c.in, got, ok, c.want)
		}
	}
}

func TestLookupFilter(t *testing.T) {
	for _, cmd := range []string{"go test ./...", "go test -v -race ./internal/x 2>&1", "cd /p && go test", "GOFLAGS=-count=1 go test"} {
		if f := lookupFilter(cmd); f == nil || f.Name != "go test" {
			t.Errorf("lookupFilter(%q) = %v, want go test", cmd, f)
		}
	}
	for _, cmd := range []string{
		"go build ./...", "go vet ./...", "make build", "cat README.md", "go test -json ./...", "go test ./... | tail", "echo go test", "",
	} {
		if f := lookupFilter(cmd); f != nil {
			t.Errorf("lookupFilter(%q) = %s, want nil (generic path)", cmd, f.Name)
		}
	}

	if lookupFilter("go test ./... -args -json") == nil {
		t.Error("-json after -args must not disable the filter")
	}
}

const goTestVerbose = `=== RUN   TestA
    a_test.go:5: loaded config
--- PASS: TestA (0.00s)
=== RUN   TestB
    b_test.go:12: connecting to db
--- FAIL: TestB (0.01s)
    b_test.go:15: want 3, got 4
=== RUN   TestC
=== RUN   TestC/sub
    c_test.go:7: warning: slow disk
    c_test.go:8: just chatting
    --- PASS: TestC/sub (0.00s)
--- PASS: TestC (0.00s)
FAIL
FAIL	example.com/x	0.012s
?   	example.com/y	[no test files]
?   	example.com/z	[no test files]
=== RUN   TestD
--- PASS: TestD (0.00s)
PASS
ok  	example.com/w	0.020s
FAIL`

func TestFilterGoTest(t *testing.T) {
	lines := strings.Split(goTestVerbose, "\n")
	got, lossy := filterGoTest(lines)
	if !lossy {
		t.Error("lines were dropped: must be lossy")
	}
	want := []string{

		"    b_test.go:12: connecting to db",
		"--- FAIL: TestB (0.01s)",
		"    b_test.go:15: want 3, got 4",
		"    c_test.go:7: warning: slow disk",
		"FAIL",
		"FAIL\texample.com/x\t0.012s",
		"ok  \texample.com/w\t0.020s",
		"FAIL",
		"[3 tests passed]",
		"[2 packages without test files]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFilterGoTestKeepsPanicAndPlainOutput(t *testing.T) {

	plain := []string{"ok  \texample.com/a\t0.1s", "ok  \texample.com/b\t(cached)"}
	got, lossy := filterGoTest(plain)
	if lossy || !reflect.DeepEqual(got, plain) {
		t.Errorf("ordinary output must stay intact: lossy=%v %q", lossy, got)
	}
	panicOut := []string{
		"=== RUN   TestX",
		"panic: boom [recovered]",
		"\tpanic: boom",
		"goroutine 7 [running]:",
		"example.com/x.TestX(0x1)",
		"\t/src/x_test.go:9 +0x1d",
		"FAIL\texample.com/x\t0.3s",
	}
	got, _ = filterGoTest(panicOut)
	if !reflect.DeepEqual(got, panicOut[1:]) {
		t.Errorf("panic and stack trace must stay intact, only === RUN is dropped:\n%s", strings.Join(got, "\n"))
	}
}

func TestFilterGoTestKeepsPASSWithoutOK(t *testing.T) {

	got, _ := filterGoTest([]string{"PASS"})
	if !reflect.DeepEqual(got, []string{"PASS"}) {
		t.Errorf("got %q", got)
	}
}

func TestBodyUsesFilterOnlyForMatchingCommand(t *testing.T) {
	raw := goTestVerbose
	out, lossy := Body(raw, Options{Budget: 8000, Command: "go test -v ./..."})
	if !lossy || strings.Contains(out, "=== RUN") || !strings.Contains(out, "[3 tests passed]") ||
		!strings.Contains(out, "--- FAIL: TestB") {
		t.Errorf("the go test filter was not used: lossy=%v\n%s", lossy, out)
	}

	out, lossy = Body(raw, Options{Budget: 8000, Command: "go test -v ./... | cat"})
	if lossy || out != raw {
		t.Errorf("commands with a pipe must not be filtered: lossy=%v", lossy)
	}
	out, lossy = Body(raw, Options{Budget: 8000})
	if lossy || out != raw {
		t.Errorf("without Command it must not be filtered: lossy=%v", lossy)
	}
}

func TestBodyFilterKeepsFailures(t *testing.T) {

	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("=== RUN   TestN" + word(i) + "\n--- PASS: TestN" + word(i) + " (0.00s)\n")
	}
	b.WriteString("=== RUN   TestBad\n    bad_test.go:3: want 1, got 2\n--- FAIL: TestBad (0.00s)\nFAIL\nFAIL\texample.com/p\t0.4s\n")
	out, _ := Body(b.String(), Options{Budget: 300, Failed: true, Command: "go test -v"})
	for _, must := range []string{"--- FAIL: TestBad", "want 1, got 2", "FAIL\texample.com/p", "[300 tests passed]"} {
		if !strings.Contains(out, must) {
			t.Errorf("%q is missing:\n%s", must, out)
		}
	}
	if len(out) > 300 {
		t.Errorf("exceeds the budget: %d", len(out))
	}
}

func TestFilterGoTestSubtestLogsConsistent(t *testing.T) {
	in := []string{
		"=== RUN   TestSubs",
		"=== RUN   TestSubs/one",
		"    s_test.go:9: sub log one",
		"=== RUN   TestSubs/two",
		"    s_test.go:9: sub log two",
		"=== RUN   TestSubs/three",
		"    s_test.go:9: sub log three",
		"--- PASS: TestSubs (0.00s)",
		"    --- PASS: TestSubs/one (0.00s)",
		"    --- PASS: TestSubs/two (0.00s)",
		"    --- PASS: TestSubs/three (0.00s)",
		"=== RUN   TestSkip",
		"    s_test.go:20: not today",
		"--- SKIP: TestSkip (0.00s)",
		"PASS",
		"ok  \texample.com/s\t0.01s",
	}
	got, lossy := filterGoTest(in)
	want := []string{
		"    s_test.go:20: not today",
		"--- SKIP: TestSkip (0.00s)",
		"ok  \texample.com/s\t0.01s",
		"[1 test passed]",
	}
	if !lossy || !reflect.DeepEqual(got, want) {
		t.Errorf("lossy=%v got:\n%s", lossy, strings.Join(got, "\n"))
	}
}

func TestFilterGoTestKeepsFailingSubtestLogs(t *testing.T) {
	in := []string{
		"=== RUN   TestSubs",
		"=== RUN   TestSubs/good",
		"    s_test.go:9: fine here",
		"=== RUN   TestSubs/bad",
		"    s_test.go:9: exploding now",
		"--- FAIL: TestSubs (0.00s)",
		"    --- PASS: TestSubs/good (0.00s)",
		"    --- FAIL: TestSubs/bad (0.00s)",
		"FAIL",
		"FAIL\texample.com/s\t0.01s",
	}
	got, _ := filterGoTest(in)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "exploding now") || strings.Contains(joined, "fine here") ||
		!strings.Contains(joined, "--- FAIL: TestSubs/bad") || !strings.Contains(joined, "FAIL\texample.com/s") {
		t.Errorf("failing subtest logs must stay, passing ones dropped:\n%s", joined)
	}
	if strings.Index(joined, "exploding now") > strings.Index(joined, "--- FAIL: TestSubs/bad") {
		t.Errorf("the log must appear before its verdict:\n%s", joined)
	}
}

func TestFilterGoTestDroppingBarePASSIsNotLossy(t *testing.T) {
	got, lossy := filterGoTest([]string{"testing: warning: no tests to run", "PASS", "ok  \texample.com/a\t0.01s [no tests to run]"})
	if lossy || len(got) != 2 {
		t.Errorf("dropping PASS lines represented by ok must not count as lossy: lossy=%v %q", lossy, got)
	}
	if got, _ := filterGoTest([]string{"?   \texample.com/y\t[no test files]"}); got[len(got)-1] != "[1 package without test files]" {
		t.Errorf("singular form: %q", got)
	}
}

func TestBodySkipsFilterWhenGainIsBelowNoteCost(t *testing.T) {

	raw := "=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\nok  \texample.com/a\t0.1s"
	out, lossy := Body(raw, Options{Budget: 8000, Command: "go test -v ./..."})
	if lossy || out != raw {
		t.Errorf("small savings must not be filtered: lossy=%v\n%s", lossy, out)
	}
}

func TestFilterGoTestPlainPrintDoesNotReleaseOtherLogs(t *testing.T) {
	in := []string{"=== RUN   TestMany"}
	for _, n := range []string{"a", "b", "c"} {
		in = append(in, "=== RUN   TestMany/"+n, "    m_test.go:9: checking case "+n)
	}
	in = append(in, "final summary: all cases checked", "--- PASS: TestMany (0.00s)")
	for _, n := range []string{"a", "b", "c"} {
		in = append(in, "    --- PASS: TestMany/"+n+" (0.00s)")
	}
	in = append(in, "PASS", "ok  \texample.com/m\t0.01s")
	got, _ := filterGoTest(in)
	want := []string{
		"    m_test.go:9: checking case c",
		"final summary: all cases checked",
		"ok  \texample.com/m\t0.01s",
		"[1 test passed]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%s", strings.Join(got, "\n"))
	}
}
