package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBasicAndEmpty(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "a.txt"), "text", 0o644)
	mustWrite(t, filepath.Join(s.Root, "empty.txt"), "", 0o644)

	res, err := callTool(t, readTool, s, readInput{Path: "a.txt"})
	if err != nil || textOf(res) != "text" {
		t.Errorf("read = %q err=%v", textOf(res), err)
	}
	res, err = callTool(t, readTool, s, readInput{Path: "empty.txt"})
	if err != nil || textOf(res) != "(empty)" {
		t.Errorf("empty read = %q err=%v", textOf(res), err)
	}
}

func TestReadOffsetLimitNextOffset(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "a.txt"), strings.Repeat("a", 100), 0o644)

	res, err := callTool(t, readTool, s, readInput{Path: "a.txt", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat("a", 10) + "\n[next_offset: 10, file size: 100]"; textOf(res) != want {
		t.Errorf("read = %q, want %q", textOf(res), want)
	}

	res, err = callTool(t, readTool, s, readInput{Path: "a.txt", Offset: 90})
	if err != nil || textOf(res) != strings.Repeat("a", 10) {
		t.Errorf("read past the end = %q err=%v", textOf(res), err)
	}
}

func TestReadCappedByMaxOutput(t *testing.T) {
	s := testSettings(t)
	s.MaxOutput = 1000
	mustWrite(t, filepath.Join(s.Root, "a.txt"), strings.Repeat("a", 5000), 0o644)
	res, err := callTool(t, readTool, s, readInput{Path: "a.txt", Limit: 999999})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(textOf(res), "[next_offset: 1000, file size: 5000]") {
		t.Errorf("tail = %q", textOf(res)[len(textOf(res))-40:])
	}
}

func TestReadNeverSplitsUTF8(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "u.txt"), "ééééé", 0o644)
	res, err := callTool(t, readTool, s, readInput{Path: "u.txt", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if want := "éé\n[next_offset: 4, file size: 10]"; textOf(res) != want {
		t.Errorf("read = %q, want %q", textOf(res), want)
	}
}

func TestReadErrors(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "bin"), "a\x00b", 0o644)
	cases := []struct {
		name string
		in   readInput
		want string
	}{
		{"binary", readInput{Path: "bin"}, "binary file"},
		{"directory", readInput{Path: "."}, "not a regular file"},
		{"does not exist", readInput{Path: "nope"}, "no such file"},
		{"negative offset", readInput{Path: "bin", Offset: -1}, "offset and limit must be >= 0"},
		{"negative limit", readInput{Path: "bin", Limit: -1}, "offset and limit must be >= 0"},
		{"empty path", readInput{}, "path is empty"},
	}
	for _, c := range cases {
		_, err := callTool(t, readTool, s, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct{ raw, want string }{
		{"a/b", "/root/a/b"},
		{"/abs/x", "/abs/x"},
		{"~", home},
		{"~/x", filepath.Join(home, "x")},
		{"a/../b", "/root/b"},
		{"~user/x", "/root/~user/x"},
	}
	for _, c := range cases {
		got, err := resolve("/root", c.raw)
		if err != nil || got != c.want {
			t.Errorf("resolve(%q) = %q err=%v, want %q", c.raw, got, err, c.want)
		}
	}
}

func TestWriteNewCreatesParents(t *testing.T) {
	s := testSettings(t)
	res, err := callTool(t, writeTool, s, writeInput{Path: "a/b/c.txt", Content: "text"})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Root, "a/b/c.txt")
	if want := "wrote 4 bytes to " + p; textOf(res) != want {
		t.Errorf("result = %q, want %q", textOf(res), want)
	}
	if mustRead(t, p) != "text" {
		t.Error("wrong file content")
	}
}

func TestWriteRefusesExistingWithoutOverwrite(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "a.txt")
	mustWrite(t, p, "old", 0o644)
	_, err := callTool(t, writeTool, s, writeInput{Path: "a.txt", Content: "new"})
	if err == nil || !strings.Contains(err.Error(), "already exists; set overwrite=true") {
		t.Errorf("err = %v", err)
	}
	if mustRead(t, p) != "old" {
		t.Error("the old file changed although it was rejected")
	}
}

func TestWriteOverwriteKeepsMode(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "secret.txt")
	mustWrite(t, p, "old", 0o600)
	if _, err := callTool(t, writeTool, s, writeInput{Path: "secret.txt", Content: "new", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if mustRead(t, p) != "new" || info.Mode().Perm() != 0o600 {
		t.Errorf("wrong content/mode: %q %v", mustRead(t, p), info.Mode().Perm())
	}
}

func TestWriteFollowsSymlink(t *testing.T) {
	s := testSettings(t)
	target := filepath.Join(s.Root, "target.txt")
	link := filepath.Join(s.Root, "link.txt")
	mustWrite(t, target, "old", 0o644)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(t, writeTool, s, writeInput{Path: "link.txt", Content: "new", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	if mustRead(t, target) != "new" {
		t.Error("the target file did not change")
	}
}

func TestWriteErrorsAndTilde(t *testing.T) {
	s := testSettings(t)
	if _, err := callTool(t, writeTool, s, writeInput{Path: "", Content: "x"}); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Errorf("empty path: err = %v", err)
	}
	if _, err := callTool(t, writeTool, s, writeInput{Path: ".", Content: "x", Overwrite: true}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory: err = %v", err)
	}

	if _, err := callTool(t, writeTool, s, writeInput{Path: "~/from-tilde.txt", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if mustRead(t, filepath.Join(home, "from-tilde.txt")) != "x" {
		t.Error("tilde was not expanded to HOME")
	}
}

func TestEditSingleAndReplaceAll(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "e.txt")
	mustWrite(t, p, "one two three", 0o640)

	res, err := callTool(t, editTool, s, editInput{Path: "e.txt", Old: "two", New: "TWO"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "replaced 1 occurrence(s) in " + p; textOf(res) != want {
		t.Errorf("result = %q, want %q", textOf(res), want)
	}
	info, _ := os.Stat(p)
	if mustRead(t, p) != "one TWO three" || info.Mode().Perm() != 0o640 {
		t.Errorf("wrong content/mode: %q %v", mustRead(t, p), info.Mode().Perm())
	}

	mustWrite(t, p, "x x x", 0o644)
	res, err = callTool(t, editTool, s, editInput{Path: "e.txt", Old: "x", New: "y", ReplaceAll: true})
	if err != nil || !strings.HasPrefix(textOf(res), "replaced 3 occurrence(s)") || mustRead(t, p) != "y y y" {
		t.Errorf("replace_all: %q err=%v content=%q", textOf(res), err, mustRead(t, p))
	}
}

func TestEditErrors(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "e.txt")
	mustWrite(t, p, "x x", 0o644)
	mustWrite(t, filepath.Join(s.Root, "large.bin"), strings.Repeat("a", maxEditBytes+1), 0o644)
	cases := []struct {
		name string
		in   editInput
		want string
	}{
		{"empty old", editInput{Path: "e.txt", Old: "", New: "y"}, "old is empty"},
		{"old equals new", editInput{Path: "e.txt", Old: "x", New: "x"}, "old and new are identical"},
		{"not found", editInput{Path: "e.txt", Old: "zzz", New: "y"}, "old text not found"},
		{"matches twice", editInput{Path: "e.txt", Old: "x", New: "y"}, "matches 2 times"},
		{"file does not exist", editInput{Path: "nope", Old: "x", New: "y"}, "no such file"},
		{"directory", editInput{Path: ".", Old: "x", New: "y"}, "not a regular file"},
		{"too large", editInput{Path: "large.bin", Old: "a", New: "b"}, "file too large to edit"},
	}
	for _, c := range cases {
		_, err := callTool(t, editTool, s, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
	if mustRead(t, p) != "x x" {
		t.Error("the file changed although every edit was rejected")
	}
}
