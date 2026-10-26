package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestDeleteMovesToTrash(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "f.txt")
	mustWrite(t, p, "content", 0o644)

	res, err := callTool(t, deleteTool, s, deleteInput{Paths: []string{"f.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	out := textOf(res)
	if !strings.HasPrefix(out, "Moved to Trash: 1 ok, 0 rejected/failed\n") || !strings.Contains(out, "[ok] "+p+" -> Trash (f.txt)") {
		t.Errorf("result = %q", out)
	}
	if res.IsError || exists(p) {
		t.Errorf("isError=%v, file still exists=%v", res.IsError, exists(p))
	}
	if mustRead(t, filepath.Join(s.TrashDir, "files", "f.txt")) != "content" {
		t.Error("the file is not in Trash")
	}
	if info := mustRead(t, filepath.Join(s.TrashDir, "info", "f.txt.trashinfo")); !strings.HasPrefix(info, "[Trash Info]\nPath=") || !strings.Contains(info, "DeletionDate=") {
		t.Errorf("wrong trashinfo: %q", info)
	}
}

func TestDeleteNameCollisionGetsSuffix(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "f.txt")
	for _, content := range []string{"one", "two"} {
		mustWrite(t, p, content, 0o644)
		if _, err := callTool(t, deleteTool, s, deleteInput{Paths: []string{"f.txt"}}); err != nil {
			t.Fatal(err)
		}
	}
	if mustRead(t, filepath.Join(s.TrashDir, "files", "f.txt")) != "one" || mustRead(t, filepath.Join(s.TrashDir, "files", "f.txt.1")) != "two" {
		t.Error("a colliding name did not get the .1 suffix")
	}
}

func TestDeleteDirectories(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "d", "x"), "x", 0o644)
	if err := os.Mkdir(filepath.Join(s.Root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, _ := callTool(t, deleteTool, s, deleteInput{Paths: []string{"d"}})
	if !res.IsError || !strings.Contains(textOf(res), "non-empty directory; set recursive=true") {
		t.Errorf("a non-empty directory must be rejected: %q", textOf(res))
	}
	if !exists(filepath.Join(s.Root, "d", "x")) {
		t.Error("a non-empty directory was deleted although it was rejected")
	}

	res, _ = callTool(t, deleteTool, s, deleteInput{Paths: []string{"d", "empty"}, Recursive: true})
	if res.IsError || !strings.HasPrefix(textOf(res), "Moved to Trash: 2 ok") {
		t.Errorf("recursive: %q", textOf(res))
	}
	if mustRead(t, filepath.Join(s.TrashDir, "files", "d", "x")) != "x" {
		t.Error("directory contents did not go to Trash")
	}

	if err := os.Mkdir(filepath.Join(s.Root, "empty2"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ = callTool(t, deleteTool, s, deleteInput{Paths: []string{"empty2"}})
	if res.IsError || !strings.HasPrefix(textOf(res), "Moved to Trash: 1 ok") {
		t.Errorf("empty directory: %q", textOf(res))
	}
}

func TestDeleteRefusesProtectedAndSystemPaths(t *testing.T) {
	s := testSettings(t)
	home, _ := os.UserHomeDir()
	cases := []struct{ path, want string }{
		{s.Root, "protected path"},
		{home, "protected path"},
		{filepath.Dir(s.TrashDir), "contains protected path"},
		{s.NotesDir, "protected path"},
		{"/", "system path"},
		{"/tmp", "system path"},
		{"/etc", "system path"},
	}
	for _, c := range cases {
		res, err := callTool(t, deleteTool, s, deleteInput{Paths: []string{c.path}, Recursive: true, Permanent: true})
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if !res.IsError || !strings.Contains(textOf(res), "[rejected] "+c.path+": "+c.want) {
			t.Errorf("%s: result = %q, want [rejected] ... %s", c.path, textOf(res), c.want)
		}
	}
	for _, d := range []string{s.Root, s.TrashDir, "/etc"} {
		if _, err := os.Stat(d); err != nil && d != s.TrashDir {
			t.Errorf("%s is missing: %v", d, err)
		}
	}
}

func TestDeleteProtectionSeesThroughSymlinkParent(t *testing.T) {
	s := testSettings(t)

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(s.Root), alias); err != nil {
		t.Fatal(err)
	}
	res, _ := callTool(t, deleteTool, s, deleteInput{Paths: []string{filepath.Join(alias, "root")}, Recursive: true})
	if !res.IsError || !strings.Contains(textOf(res), "protected path") {
		t.Errorf("slipped through a symlink: %q", textOf(res))
	}
	if !exists(s.Root) {
		t.Error("Root was deleted through a symlink")
	}
}

func TestDeleteSymlinkRemovesLinkOnly(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "d", "x"), "x", 0o644)
	link := filepath.Join(s.Root, "link")
	if err := os.Symlink(filepath.Join(s.Root, "d"), link); err != nil {
		t.Fatal(err)
	}
	res, _ := callTool(t, deleteTool, s, deleteInput{Paths: []string{"link"}})
	if res.IsError || exists(link) || !exists(filepath.Join(s.Root, "d", "x")) {
		t.Errorf("only the link may disappear: %q", textOf(res))
	}
}

func TestDeleteDuplicateAndMissing(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "f"), "x", 0o644)
	res, _ := callTool(t, deleteTool, s, deleteInput{Paths: []string{"f", "f", "*.txt"}})
	out := textOf(res)
	if res.IsError {
		t.Error("duplicates and not-found entries are only skipped, not errors")
	}
	for _, want := range []string{"1 ok, 0 rejected/failed, 2 skipped", ": duplicate", "wildcards are not expanded"} {
		if !strings.Contains(out, want) {
			t.Errorf("result does not contain %q: %q", want, out)
		}
	}
}

func TestDeletePermanent(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "f")
	mustWrite(t, p, "x", 0o644)
	res, _ := callTool(t, deleteTool, s, deleteInput{Paths: []string{"f"}, Permanent: true})
	if !strings.HasPrefix(textOf(res), "Deleted permanently: 1 ok") || !strings.Contains(textOf(res), "(deleted permanently)") || exists(p) {
		t.Errorf("result = %q, file exists=%v", textOf(res), exists(p))
	}
	if exists(s.TrashDir) {
		t.Error("permanent must not create the Trash directory")
	}
}

func TestDeleteInsideTrash(t *testing.T) {
	s := testSettings(t)
	mustWrite(t, filepath.Join(s.Root, "f"), "x", 0o644)
	if _, err := callTool(t, deleteTool, s, deleteInput{Paths: []string{"f"}}); err != nil {
		t.Fatal(err)
	}
	inTrash := filepath.Join(s.TrashDir, "files", "f")

	res, _ := callTool(t, deleteTool, s, deleteInput{Paths: []string{inTrash}})
	if !res.IsError || !strings.Contains(textOf(res), "already in Trash; use permanent=true") || !exists(inTrash) {
		t.Errorf("delete from Trash without permanent: %q", textOf(res))
	}

	res, _ = callTool(t, deleteTool, s, deleteInput{Paths: []string{inTrash}, Permanent: true})
	if res.IsError || exists(inTrash) || exists(filepath.Join(s.TrashDir, "info", "f.trashinfo")) {
		t.Errorf("permanent delete from Trash must also remove the trashinfo: %q", textOf(res))
	}
}

func TestDeleteInputLimits(t *testing.T) {
	s := testSettings(t)
	if _, err := callTool(t, deleteTool, s, deleteInput{}); err == nil || !strings.Contains(err.Error(), "paths must have 1-50 entries") {
		t.Errorf("empty paths: err = %v", err)
	}
	many := make([]string, maxDeletePaths+1)
	for i := range many {
		many[i] = "f"
	}
	if _, err := callTool(t, deleteTool, s, deleteInput{Paths: many}); err == nil || !strings.Contains(err.Error(), "paths must have 1-50 entries") {
		t.Errorf("too many: err = %v", err)
	}
	if _, err := callTool(t, deleteTool, s, deleteInput{Paths: []string{""}}); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Errorf("empty path: err = %v", err)
	}
}
