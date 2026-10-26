package rawstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSaveWritesPrivateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	path, err := New(dir).Save([]byte("exit 0 (1ms)\nhello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "run-") || !strings.HasSuffix(path, ".txt") {
		t.Errorf("path = %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "exit 0 (1ms)\nhello\n" {
		t.Errorf("content = %q, err = %v", got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700", di.Mode().Perm())
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want 1", len(entries))
	}
}

func TestNilStoreIsDisabled(t *testing.T) {
	if s := New(""); s != nil {
		t.Fatalf("New(\"\") = %v, want nil", s)
	}
	if _, err := New("").Save([]byte("x")); !errors.Is(err, ErrDisabled) {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}

func TestPruneRemovesOldRunFilesOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldRun := write("run-20200101-000000-aaaaaa.txt", 8*24*time.Hour)
	freshRun := write("run-20261020-000000-bbbbbb.txt", 24*time.Hour)
	oldOther := write("screenshot.png", 30*24*time.Hour)

	if _, err := New(dir).Save([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldRun); !os.IsNotExist(err) {
		t.Error("run-* files older than 7 days must be removed")
	}
	for _, p := range []string{freshRun, oldOther} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must not be removed: %v", filepath.Base(p), err)
		}
	}
}

func TestPruneCapsFileCount(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.maxFiles = 3
	var names []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "run-2026102"+string(rune('0'+i))+"-000000-aaaaaa.txt")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(5-i) * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		names = append(names, p)
	}
	if _, err := s.Save([]byte("new")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatalf("after Save there are %d files, want 3 (limit)", len(entries))
	}
	for _, p := range names[:3] {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s (oldest) must be removed", filepath.Base(p))
		}
	}
	for _, p := range names[3:] {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s (newest) must still exist: %v", filepath.Base(p), err)
		}
	}
}

func TestSaveParallelGivesDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	var wg sync.WaitGroup
	paths := make([]string, 20)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := s.Save([]byte("x"))
			if err != nil {
				t.Error(err)
			}
			paths[i] = p
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Errorf("duplicate path: %s", p)
		}
		seen[p] = true
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 20 {
		t.Errorf("found %d files, want 20", len(entries))
	}
}
