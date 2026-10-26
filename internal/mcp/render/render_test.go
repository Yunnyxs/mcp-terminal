package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/proto"
)

func TestSummarizeParams(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"zeta":{},"alpha":{},"mid":{}},"required":["zeta"]}`)
	if got := SummarizeParams(schema); got != "zeta*, alpha, mid" {
		t.Errorf("got %q", got)
	}
	for _, s := range []string{``, `null`, `{}`, `{"type":"object"}`, `not json`} {
		if got := SummarizeParams(json.RawMessage(s)); got != "" {
			t.Errorf("SummarizeParams(%q) = %q, want empty", s, got)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := FirstLine("  one\ntwo", 50); got != "one" {
		t.Errorf("got %q", got)
	}
	if got := FirstLine(strings.Repeat("é", 20), 10); utf8.RuneCountInString(got) != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("got %q", got)
	}
}

func TestToolsAndToolDetail(t *testing.T) {
	list := []proto.ToolInfo{
		{Name: "echo", Description: "Return text.\nSecond line.", Schema: json.RawMessage(`{"properties":{"text":{}},"required":["text"]}`)},
		{Name: "ping"},
	}
	if got, want := Tools(list), "- echo(text*): Return text.\n- ping()\n"; got != want {
		t.Errorf("Tools = %q, want %q", got, want)
	}
	d := ToolDetail(list[0])
	for _, want := range []string{"echo\n", "Return text.\nSecond line.", "inputSchema:", `"text"`} {
		if !strings.Contains(d, want) {
			t.Errorf("ToolDetail does not contain %q:\n%s", want, d)
		}
	}
}

func outPaths(t *testing.T) config.Paths {
	return config.Paths{OutDir: filepath.Join(t.TempDir(), "out")}
}

func TestResultTruncatesOnRuneBoundary(t *testing.T) {
	p := outPaths(t)
	full := strings.Repeat("é", 3000)
	for _, max := range []int{1001, 1000, 2501} {
		out, err := Result(p, "m", "t/x y", &proto.CallResult{Parts: []proto.Part{{Kind: "text", Text: full}}}, max)
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(out) {
			t.Errorf("max=%d: output is not valid UTF-8", max)
		}
		if !strings.Contains(out, "output truncated") {
			t.Errorf("max=%d: no truncation marker", max)
		}
	}
	files, _ := os.ReadDir(p.OutDir)
	if len(files) == 0 {
		t.Fatal("full result file was not saved")
	}
	for _, f := range files {
		if strings.ContainsAny(f.Name(), "/ ") {
			t.Errorf("unsafe file name: %q", f.Name())
		}
		if fi, _ := f.Info(); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions %o, want 600", f.Name(), fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(p.OutDir); fi.Mode().Perm() != 0o700 {
		t.Errorf("directory permissions = %o, want 700", fi.Mode().Perm())
	}
}

func TestResultKinds(t *testing.T) {
	p := outPaths(t)
	cases := []struct {
		name string
		in   *proto.CallResult
		want []string
	}{
		{"two texts", &proto.CallResult{Parts: []proto.Part{{Kind: "text", Text: "a"}, {Kind: "text", Text: "b"}}}, []string{"a\nb"}},
		{"image saved", &proto.CallResult{Parts: []proto.Part{{Kind: "image", MIME: "image/png", Data: []byte{1, 2, 3}}}}, []string{"[image saved:", ".png", "image/png, 3 bytes"}},
		{"empty binary", &proto.CallResult{Parts: []proto.Part{{Kind: "audio"}}}, []string{"[audio empty]"}},
		{"link", &proto.CallResult{Parts: []proto.Part{{Kind: "link", URI: "http://x/y", Text: " name "}}}, []string{"[link: http://x/y name]"}},
		{"text resource", &proto.CallResult{Parts: []proto.Part{{Kind: "resource", URI: "file:///a", Text: "content"}}}, []string{"[resource: file:///a]\ncontent"}},
		{"structured", &proto.CallResult{Structured: json.RawMessage(`{"a":1}`)}, []string{`"a": 1`}},
		{"structured invalid", &proto.CallResult{Structured: json.RawMessage(`{notjson`)}, []string{`{notjson`}},
		{"empty", &proto.CallResult{}, []string{""}},
	}
	for _, c := range cases {
		out, err := Result(p, "m", "t", c.in, 1000)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: %q does not contain %q", c.name, out, w)
			}
		}
	}
}

func TestPruneOutRemovesOnlyOldRegularFiles(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, "old.txt"), filepath.Join(dir, "fresh.txt")
	for _, f := range []string{old, fresh} {
		_ = os.WriteFile(f, []byte("x"), 0o600)
	}
	past := time.Now().Add(-(outKeepDays + 1) * 24 * time.Hour)
	_ = os.Chtimes(old, past, past)
	sub := filepath.Join(dir, "sub")
	_ = os.Mkdir(sub, 0o700)
	_ = os.Chtimes(sub, past, past)

	pruneOut(dir)
	if _, err := os.Stat(old); err == nil {
		t.Error("old file must be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh file must remain")
	}
	if _, err := os.Stat(sub); err != nil {
		t.Error("directory must not be removed")
	}
}

func TestSafeToolAndExt(t *testing.T) {
	if got := safeTool("a/b c.d"); got != "a_b_c_d" {
		t.Errorf("safeTool = %q", got)
	}
	if got := safeTool(strings.Repeat("x", 60)); len(got) != 40 {
		t.Errorf("safeTool length = %d", len(got))
	}
	for mime, ext := range map[string]string{"image/png": ".png", "IMAGE/JPEG; q=1": ".jpg", "application/pdf": ".pdf", "x/unknown": ".bin", "": ".bin"} {
		if got := extFor(mime); got != ext {
			t.Errorf("extFor(%q) = %q, want %q", mime, got, ext)
		}
	}
}
