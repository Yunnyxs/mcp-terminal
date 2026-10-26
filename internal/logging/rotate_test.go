package logging

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func segmentFiles(t *testing.T, dir, base string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() == base || strings.HasPrefix(e.Name(), base+".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func totalSize(t *testing.T, dir string, names []string) int64 {
	t.Helper()
	var n int64
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		n += info.Size()
	}
	return n
}

func TestOpenCreatesFolderAndFileWithTightPerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "inner")
	path := filepath.Join(dir, "a.log")
	r, err := Open(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("log file: err=%v info=%v, want perm 600", err, info)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("log directory: err=%v info=%v, want perm 700", err, info)
	}
}

func TestOpenTightensLooseExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 600", info.Mode().Perm())
	}
	if _, err := r.Write([]byte("new\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old\nnew\n" {
		t.Errorf("content = %q: old file must be appended to, not overwritten", got)
	}
}

func TestWriteKeepsEntriesWholeAcrossSegments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	r, err := Open(path, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	const n = 300
	for i := 0; i < n; i++ {
		line := fmt.Sprintf("entry-%04d %s\n", i, strings.Repeat("x", 80))
		if w, err := r.Write([]byte(line)); err != nil || w != len(line) {
			t.Fatalf("Write %d: w=%d err=%v", i, w, err)
		}
	}
	files := segmentFiles(t, dir, "a.log")
	if len(files) < 3 {
		t.Fatalf("only %d files: rotation did not happen (%v)", len(files), files)
	}
	seen := map[string]bool{}
	for _, name := range files {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := sc.Text()
			if !strings.HasPrefix(l, "entry-") || !strings.HasSuffix(l, strings.Repeat("x", 80)) {
				t.Errorf("%s: line split or corrupted: %q", name, l)
			}
			seen[l[:10]] = true
		}
		f.Close()
	}
	if len(seen) != n {
		t.Errorf("found %d of %d entries: some missing or duplicated", len(seen), n)
	}
}

func TestTotalSizeStaysWithinLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	const limit = 4000
	r, err := Open(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 500; i++ {
		if _, err := r.Write([]byte(fmt.Sprintf("%04d %s\n", i, strings.Repeat("y", 55)))); err != nil {
			t.Fatal(err)
		}
		if got := totalSize(t, dir, segmentFiles(t, dir, "a.log")); got > limit {
			t.Fatalf("after entry %d total %d exceeds limit %d", i, got, limit)
		}
	}

	var all strings.Builder
	for _, name := range segmentFiles(t, dir, "a.log") {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		all.Write(b)
	}
	if !strings.Contains(all.String(), "0499 ") {
		t.Error("last entry is missing from the log")
	}
	if strings.Contains(all.String(), "0000 ") {
		t.Error("oldest entry still present: pruning did not run")
	}
}

func TestPruneOnlyTouchesOwnSegments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")

	keep := []string{"a.log.bak", "a.log.20260101", "a.log.notatime", "b.log", "a.logx", "a.log.20260101-000000.000000.gz"}
	for _, k := range keep {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(strings.Repeat("z", 500)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(path, 2000)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 200; i++ {
		if _, err := r.Write([]byte(strings.Repeat("w", 90) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range keep {
		if _, err := os.Stat(filepath.Join(dir, k)); err != nil {
			t.Errorf("%s was also deleted: %v", k, err)
		}
	}
}

func TestReopenContinuesActiveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	r1, err := Open(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r1.Write([]byte("one\n"))
	r1.Close()
	r2, err := Open(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	r2.Write([]byte("two\n"))
	if got, _ := os.ReadFile(path); string(got) != "one\ntwo\n" {
		t.Errorf("content = %q", got)
	}
	if r2.size != int64(len("one\ntwo\n")) {
		t.Errorf("size = %d: old file size must be counted", r2.size)
	}
}

func TestConcurrentWritesAreNotInterleaved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	r, err := Open(path, 50_000_000)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	const workers, each = 8, 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				line := fmt.Sprintf("w%d-%04d %s\n", w, i, strings.Repeat("c", 200))
				if _, err := r.Write([]byte(line)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	count := 0
	for _, name := range segmentFiles(t, dir, "a.log") {
		f, _ := os.Open(filepath.Join(dir, name))
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := sc.Text()
			if len(l) != len("w0-0000 ")+200 || !strings.HasSuffix(l, strings.Repeat("c", 200)) {
				t.Errorf("interleaved line: %q", l)
			}
			count++
		}
		f.Close()
	}
	if count != workers*each {
		t.Errorf("lines = %d, want %d", count, workers*each)
	}
}

func TestOpenFailsWhenFolderIsAFile(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plainfile")
	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(base, "a.log"), 1<<20); err == nil {
		t.Error("Open must fail when the parent is a regular file")
	}
}
