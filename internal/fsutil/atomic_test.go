package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func noTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("temporary file left over: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicNewAndReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")

	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("dua"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "dua" {
		t.Fatalf("content = %q err=%v", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	noTemp(t, dir)
}

func TestWriteFileAtomicFailureLeavesNothing(t *testing.T) {

	if err := WriteFileAtomic(filepath.Join(t.TempDir(), "nope", "x"), []byte("x"), 0o600); err == nil {
		t.Error("must fail when the parent directory does not exist")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "content"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("x"), 0o600); err == nil {
		t.Error("must fail when the target is a non-empty directory")
	}
	noTemp(t, dir)
}

func TestWriteFileLikeCopiesModeAndOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o750|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	old, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := WriteFileLike(path, []byte("new"), old); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content = %q", got)
	}
	now, _ := os.Stat(path)
	if now.Mode() != old.Mode() {
		t.Errorf("mode = %v, want %v", now.Mode(), old.Mode())
	}
	o, n := old.Sys().(*syscall.Stat_t), now.Sys().(*syscall.Stat_t)
	if o.Uid != n.Uid || o.Gid != n.Gid {
		t.Errorf("owner changed: %d:%d -> %d:%d", o.Uid, o.Gid, n.Uid, n.Gid)
	}
	noTemp(t, dir)
}
