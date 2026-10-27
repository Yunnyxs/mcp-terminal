package tools

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func blobFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	matches, err := filepath.Glob(filepath.Join(dir, "blobs", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if info, err := os.Lstat(m); err == nil && info.Mode().IsRegular() {
			out = append(out, m)
		}
	}
	return out
}

func findEntry(snap snapshot, path string) (snapEntry, bool) {
	for _, e := range snap.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return snapEntry{}, false
}

func TestCheckpointNameValidation(t *testing.T) {
	for _, name := range []string{"a", "snap-1", "v1.2_final", "A.b", strings.Repeat("x", 64)} {
		if err := validCheckpointName(name); err != nil {
			t.Errorf("%q should be valid: %v", name, err)
		}
	}
	for _, name := range []string{"", ".hidden", "..", "../x", "a/b", "a b", "ü", strings.Repeat("x", 65)} {
		if err := validCheckpointName(name); err == nil {
			t.Errorf("%q should be rejected", name)
		}
	}
	dir := t.TempDir()
	if _, err := loadSnapshot(dir, "../x"); err == nil {
		t.Error("loadSnapshot accepted ../x")
	}
	if err := deleteSnapshot(dir, "../x"); err == nil {
		t.Error("deleteSnapshot accepted ../x")
	}
	if _, _, err := takeSnapshot(dir, t.TempDir(), "../x"); err == nil {
		t.Error("takeSnapshot accepted ../x")
	}
	for _, h := range []string{"", "abc", "../../etc/passwd", strings.Repeat("g", 64)} {
		if err := verifyBlob(dir, h); err == nil {
			t.Errorf("verifyBlob accepted hash %q", h)
		}
		if err := restoreBlob(dir, h, filepath.Join(t.TempDir(), "o")); err == nil {
			t.Errorf("restoreBlob accepted hash %q", h)
		}
	}
}

func TestSnapshotDedupSameContent(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "same", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "same", 0o600)
	mustWrite(t, filepath.Join(src, "c.txt"), "other", 0o755)

	snap, stats, err := takeSnapshot(cp, src, "one")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(blobFiles(t, cp)); n != 2 {
		t.Errorf("blobs = %d, want 2", n)
	}
	a, _ := findEntry(snap, "a.txt")
	b, _ := findEntry(snap, "sub/b.txt")
	if a.Hash == "" || a.Hash != b.Hash {
		t.Errorf("hash a=%q b=%q, want equal and not empty", a.Hash, b.Hash)
	}
	if stats.Files != 3 || stats.Dirs != 1 || stats.Bytes != 13 || stats.NewBytes != 9 {
		t.Errorf("stats = %+v", stats)
	}
	if b.Mode != 0o600 || b.Size != 4 || b.Kind != "file" {
		t.Errorf("entry b = %+v", b)
	}
	if c, _ := findEntry(snap, "c.txt"); c.Mode != 0o755 {
		t.Errorf("mode c = %o", c.Mode)
	}
	for i := 1; i < len(snap.Entries); i++ {
		if snap.Entries[i-1].Path >= snap.Entries[i].Path {
			t.Errorf("entries not sorted: %q before %q", snap.Entries[i-1].Path, snap.Entries[i].Path)
		}
	}
}

func TestSnapshotSharedContentAddsNoBlob(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "hello", 0o644)
	if _, _, err := takeSnapshot(cp, src, "one"); err != nil {
		t.Fatal(err)
	}
	before := len(blobFiles(t, cp))
	_, stats, err := takeSnapshot(cp, src, "two")
	if err != nil {
		t.Fatal(err)
	}
	if after := len(blobFiles(t, cp)); after != before {
		t.Errorf("blobs %d -> %d, want unchanged", before, after)
	}
	if stats.NewBytes != 0 || stats.Bytes != 5 {
		t.Errorf("stats = %+v", stats)
	}
	list, err := listSnapshots(cp)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func TestSnapshotExistingNameAndBadSource(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "v1", 0o644)
	if _, _, err := takeSnapshot(cp, src, "x"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "a.txt"), "v2", 0o644)
	if _, _, err := takeSnapshot(cp, src, "x"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want already exists", err)
	}
	old, err := loadSnapshot(cp, "x")
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := findEntry(old, "a.txt"); e.Size != 2 {
		t.Errorf("snapshot was overwritten: %+v", e)
	}
	file := filepath.Join(src, "a.txt")
	if _, _, err := takeSnapshot(cp, file, "y"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("err = %v, want not a directory", err)
	}
	if _, _, err := takeSnapshot(cp, filepath.Join(src, "nope"), "z"); err == nil {
		t.Error("missing source accepted")
	}
}

func TestSnapshotUnreadableFileAborts(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read everything")
	}
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "ok.txt"), "ok", 0o644)
	secret := filepath.Join(src, "secret.txt")
	mustWrite(t, secret, "x", 0o000)

	_, _, err := takeSnapshot(cp, src, "bad")
	if err == nil || !strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v, want it to mention %s", err, secret)
	}
	if exists(filepath.Join(cp, "snapshots", "bad.json")) {
		t.Error("partial snapshot was written")
	}
}

func TestSnapshotSymlinkNotFollowed(t *testing.T) {
	cp, src, outside := t.TempDir(), t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.txt"), "outside", 0o644)
	if err := os.Symlink(outside, filepath.Join(src, "link-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(src, "link-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(src, "dangling")); err != nil {
		t.Fatal(err)
	}

	snap, stats, err := takeSnapshot(cp, src, "s")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Symlinks != 3 || stats.Files != 0 || stats.Dirs != 0 {
		t.Errorf("stats = %+v", stats)
	}
	if n := len(blobFiles(t, cp)); n != 0 {
		t.Errorf("blobs = %d, a symlink target was read", n)
	}
	for _, e := range snap.Entries {
		if strings.HasPrefix(e.Path, "link-dir/") {
			t.Errorf("followed a directory symlink: %q", e.Path)
		}
	}
	if e, ok := findEntry(snap, "link-dir"); !ok || e.Kind != "symlink" || e.Target != outside {
		t.Errorf("link-dir = %+v", e)
	}
	if e, ok := findEntry(snap, "dangling"); !ok || e.Target != "missing-target" {
		t.Errorf("dangling = %+v", e)
	}
}

func TestSnapshotEmptyDirsAndSpecialFiles(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "empty", "deeper"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	snap, stats, err := takeSnapshot(cp, src, "s")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Dirs != 2 || stats.Skipped != 1 || stats.Files != 0 {
		t.Errorf("stats = %+v", stats)
	}
	if _, ok := findEntry(snap, "pipe"); ok {
		t.Error("fifo was recorded")
	}
	if e, ok := findEntry(snap, "empty/deeper"); !ok || e.Kind != "dir" {
		t.Errorf("empty/deeper = %+v", e)
	}
}

func TestSnapshotSkipsCheckpointDirInsideSource(t *testing.T) {
	src := t.TempDir()
	cp := filepath.Join(src, "Checkpoints")
	mustWrite(t, filepath.Join(src, "a.txt"), "data", 0o644)

	if _, _, err := takeSnapshot(cp, src, "first"); err != nil {
		t.Fatal(err)
	}
	snap, _, err := takeSnapshot(cp, src, "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range snap.Entries {
		if e.Path == "Checkpoints" || strings.HasPrefix(e.Path, "Checkpoints/") {
			t.Errorf("checkpoint folder included: %q", e.Path)
		}
	}
	if len(snap.Entries) != 1 || snap.Entries[0].Path != "a.txt" {
		t.Errorf("entries = %+v", snap.Entries)
	}
}

func TestListSnapshotsNewestFirstAndDelete(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a"), "1", 0o644)
	if list, err := listSnapshots(cp); err != nil || len(list) != 0 {
		t.Fatalf("empty list = %v, %v", list, err)
	}
	for _, n := range []string{"a", "b"} {
		if _, _, err := takeSnapshot(cp, src, n); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := loadSnapshot(cp, "a")
	if err != nil || snap.Name != "a" || snap.Source != src {
		t.Fatalf("load = %+v, %v", snap, err)
	}
	if _, err := loadSnapshot(cp, "nope"); err == nil {
		t.Error("loading a missing checkpoint succeeded")
	}
	if err := deleteSnapshot(cp, "a"); err != nil {
		t.Fatal(err)
	}
	if err := deleteSnapshot(cp, "a"); err == nil {
		t.Error("deleting twice succeeded")
	}
	list, err := listSnapshots(cp)
	if err != nil || len(list) != 1 || list[0].Name != "b" {
		t.Errorf("list = %+v, %v", list, err)
	}
	if n := len(blobFiles(t, cp)); n != 1 {
		t.Errorf("deleteSnapshot must keep blobs, have %d", n)
	}
}

func TestRestoreBlobAndCorruptionRejected(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "payload", 0o644)
	snap, _, err := takeSnapshot(cp, src, "s")
	if err != nil {
		t.Fatal(err)
	}
	hash := snap.Entries[0].Hash
	out := t.TempDir()
	dst := filepath.Join(out, "restored.txt")

	if err := verifyBlob(cp, hash); err != nil {
		t.Fatal(err)
	}
	if err := restoreBlob(cp, hash, dst); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, dst) != "payload" {
		t.Error("restored content differs")
	}

	bp := blobPath(cp, hash)
	if err := os.Chmod(bp, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bp, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBlob(cp, hash); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("verifyBlob err = %v", err)
	}
	dst2 := filepath.Join(out, "second.txt")
	if err := restoreBlob(cp, hash, dst2); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("restoreBlob err = %v", err)
	}
	if exists(dst2) {
		t.Error("dst was written from a corrupt blob")
	}
	if err := restoreBlob(cp, hash, dst); err == nil {
		t.Error("restoreBlob over an existing dst accepted a corrupt blob")
	}
	if mustRead(t, dst) != "payload" {
		t.Error("existing dst was overwritten by a corrupt blob")
	}
	left, _ := os.ReadDir(out)
	if len(left) != 1 {
		t.Errorf("leftover temp files in dst folder: %v", left)
	}
}

func TestGCRemovesOrphansKeepsUsed(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "keep.txt"), "keep me", 0o644)
	mustWrite(t, filepath.Join(src, "gone.txt"), "orphan soon", 0o644)
	if _, _, err := takeSnapshot(cp, src, "full"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(src, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	keep, _, err := takeSnapshot(cp, src, "keep")
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteSnapshot(cp, "full"); err != nil {
		t.Fatal(err)
	}
	if n := len(blobFiles(t, cp)); n != 2 {
		t.Fatalf("blobs before gc = %d", n)
	}

	removed, freed, err := gcBlobs(cp)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || freed != int64(len("orphan soon")) {
		t.Errorf("removed=%d freed=%d", removed, freed)
	}
	files := blobFiles(t, cp)
	if len(files) != 1 || filepath.Base(files[0]) != keep.Entries[0].Hash {
		t.Errorf("blobs after gc = %v", files)
	}
	dirs, _ := filepath.Glob(filepath.Join(cp, "blobs", "*"))
	if len(dirs) != 1 {
		t.Errorf("empty prefix folders not cleaned: %v", dirs)
	}
	if err := verifyBlob(cp, keep.Entries[0].Hash); err != nil {
		t.Error(err)
	}
}

func TestGCStopsOnBrokenSnapshotJSON(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "data", 0o644)
	if _, _, err := takeSnapshot(cp, src, "good"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(cp, "snapshots", "broken.json"), "{not json", 0o600)
	orphan := strings.Repeat("ab", 32)
	mustWrite(t, filepath.Join(cp, "blobs", "ab", orphan), "orphan", 0o400)
	before := len(blobFiles(t, cp))

	removed, _, err := gcBlobs(cp)
	if err == nil || !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("err = %v, want a mention of broken.json", err)
	}
	if removed != 0 || len(blobFiles(t, cp)) != before {
		t.Errorf("gc removed blobs (%d) despite the broken JSON", removed)
	}
}

func TestScanCurrentStoresNothingAndMatchesSnapshot(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "aaa", 0o644)
	mustWrite(t, filepath.Join(src, "d", "b.txt"), "bbb", 0o600)
	snap, _, err := takeSnapshot(cp, src, "s")
	if err != nil {
		t.Fatal(err)
	}
	before := len(blobFiles(t, cp))
	mustWrite(t, filepath.Join(src, "new.txt"), "brand new", 0o644)

	entries, stats, err := scanCurrent(cp, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobFiles(t, cp)) != before {
		t.Error("scanCurrent stored a blob")
	}
	if stats.NewBytes != 0 || stats.Files != 3 {
		t.Errorf("stats = %+v", stats)
	}
	for _, e := range snap.Entries {
		got, ok := findEntry(snapshot{Entries: entries}, e.Path)
		if !ok || got.Hash != e.Hash || got.Kind != e.Kind {
			t.Errorf("entry %s differs: %+v vs %+v", e.Path, got, e)
		}
	}
}

func TestTakeSnapshotScopedRecordsScopeOnly(t *testing.T) {
	cp, src := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "a", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "b", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "deep", "c.txt"), "c", 0o644)
	mustWrite(t, filepath.Join(src, "other", "o.txt"), "o", 0o644)

	snap, _, err := takeSnapshotScoped(cp, src, "part", []string{"sub", "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range snap.Entries {
		paths = append(paths, e.Path)
	}
	if got := strings.Join(paths, ","); got != "a.txt,sub,sub/b.txt,sub/deep,sub/deep/c.txt" {
		t.Errorf("entries = %s", got)
	}
	if len(snap.Partial) != 2 {
		t.Errorf("Partial = %v", snap.Partial)
	}
	if n := len(blobFiles(t, cp)); n != 3 {
		t.Errorf("blobs = %d, want 3", n)
	}
	loaded, err := loadSnapshot(cp, "part")
	if err != nil || len(loaded.Partial) != 2 {
		t.Errorf("loaded = %+v, %v", loaded.Partial, err)
	}

	empty, _, err := takeSnapshotScoped(cp, src, "none", []string{"missing"})
	if err != nil || len(empty.Entries) != 0 || len(empty.Partial) != 1 {
		t.Errorf("scope without matches = %+v, %v", empty, err)
	}
}

func TestSnapshotJSONWithoutPartialFieldStillLoads(t *testing.T) {
	cp := t.TempDir()
	legacy := `{"name":"old","source":"/x","created":"2026-01-01T00:00:00Z","entries":[{"path":"a","kind":"dir","mode":493,"mtime":1,"uid":0,"gid":0}]}`
	mustWrite(t, filepath.Join(cp, "snapshots", "old.json"), legacy, 0o600)
	snap, err := loadSnapshot(cp, "old")
	if err != nil || snap.Partial != nil || len(snap.Entries) != 1 {
		t.Errorf("snap = %+v, %v", snap, err)
	}
}
