package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRunHeaderAndOutput(t *testing.T) {
	s := testSettings(t)
	res, err := runCommand(context.Background(), s, runInput{Command: "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Error("exit 0 must not be IsError")
	}
	if !regexp.MustCompile(`^exit 0 \([^)]+\)\nhello\n$`).MatchString(textOf(res)) {
		t.Errorf("wrong format: %q", textOf(res))
	}
}

func TestRunNoOutputAndExitCode(t *testing.T) {
	s := testSettings(t)
	res, err := runCommand(context.Background(), s, runInput{Command: "exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^exit 3 \([^)]+\)\n\(no output\)$`).MatchString(textOf(res)) {
		t.Errorf("wrong format: %q", textOf(res))
	}
	if res.IsError {
		t.Error("a non-zero exit is not IsError: only a timeout is IsError")
	}
}

func TestRunStderrMergedAndCwd(t *testing.T) {
	s := testSettings(t)
	sub := filepath.Join(s.Root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := runCommand(context.Background(), s, runInput{Command: "pwd; echo err >&2", Cwd: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	if out := textOf(res); !strings.Contains(out, sub+"\n") || !strings.Contains(out, "err\n") {
		t.Errorf("wrong cwd or stderr: %q", out)
	}
}

func TestRunTimeout(t *testing.T) {
	s := testSettings(t)
	res, err := runCommand(context.Background(), s, runInput{Command: "sleep 5", Timeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.HasPrefix(textOf(res), "timeout after 1s, killed") {
		t.Errorf("wrong timeout result: isError=%v %q", res.IsError, textOf(res))
	}
}

func TestRunTruncatesMiddle(t *testing.T) {
	s := testSettings(t)
	s.MaxOutput = 1000
	res, err := runCommand(context.Background(), s, runInput{Command: "head -c 5000 /dev/zero | tr '\\0' x"})
	if err != nil {
		t.Fatal(err)
	}
	out := textOf(res)
	if !strings.Contains(out, "bytes truncated") {
		t.Errorf("truncation marker is missing: %q", out[:min(80, len(out))])
	}
	if len(out) > 1300 {
		t.Errorf("output was not limited: %d bytes", len(out))
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	s := testSettings(t)
	file := filepath.Join(s.Root, "f")
	mustWrite(t, file, "x", 0o600)
	cases := []struct {
		name string
		in   runInput
		want string
	}{
		{"empty command", runInput{Command: "  "}, "command is empty"},
		{"cwd is not a directory", runInput{Command: "true", Cwd: "f"}, "cwd is not a directory"},
		{"cwd does not exist", runInput{Command: "true", Cwd: "nope"}, "cwd is not a directory"},
		{"negative timeout", runInput{Command: "true", Timeout: -1}, "timeout must be 1-3600"},
		{"timeout too large", runInput{Command: "true", Timeout: 3601}, "timeout must be 1-3600"},
	}
	for _, c := range cases {
		_, err := runCommand(context.Background(), s, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
}

func TestCaptureKeepsHeadAndTail(t *testing.T) {
	c := newCapture(10)
	_, _ = c.Write([]byte("AAAAA"))
	_, _ = c.Write([]byte("bbbbbbbbbbbbbbb"))
	_, _ = c.Write([]byte("ZZZZZ"))
	got := c.String()
	if !strings.HasPrefix(got, "AAAAA") || !strings.HasSuffix(got, "ZZZZZ") || !strings.Contains(got, "bytes truncated") {
		t.Errorf("capture = %q", got)
	}
}
