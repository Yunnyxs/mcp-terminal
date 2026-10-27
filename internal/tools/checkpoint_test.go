package tools

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"terminal-mcp/internal/config"
)

func cpDo(t *testing.T, s config.Settings, in checkpointInput) (string, bool, error) {
	t.Helper()
	res, err := callTool(t, checkpointTool, s, in)
	if err != nil {
		return "", false, err
	}
	return textOf(res), res.IsError, nil
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cpOK(t *testing.T, s config.Settings, in checkpointInput) string {
	t.Helper()
	out, isErr, err := cpDo(t, s, in)
	if err != nil || isErr {
		t.Fatalf("checkpoint %+v: err=%v isError=%v out=%q", in, err, isErr, out)
	}
	return out
}

func cpNames(t *testing.T, s config.Settings) []string {
	t.Helper()
	snaps, err := listSnapshots(s.CheckpointDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sn := range snaps {
		out = append(out, sn.Name)
	}
	return out
}

func cpPreRestore(t *testing.T, s config.Settings, base string) string {
	t.Helper()
	for _, n := range cpNames(t, s) {
		if strings.HasPrefix(n, "pre-restore-"+base+"-") {
			return n
		}
	}
	t.Fatalf("no pre-restore-%s-* checkpoint in %v", base, cpNames(t, s))
	return ""
}

func treeState(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		extra := ""
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			extra, _ = os.Readlink(p)
		case fi.Mode().IsRegular():
			extra = mustRead(t, p)
		}
		fmt.Fprintf(&b, "%s|%s|%o|%s\n", rel, fi.Mode().Type(), fi.Mode().Perm(), extra)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func cpProject(t *testing.T, s config.Settings) string {
	t.Helper()
	src := filepath.Join(s.Root, "proj")
	mustWrite(t, filepath.Join(src, "a.txt"), "A1", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "b.sh"), "B1", 0o755)
	mustWrite(t, filepath.Join(src, "gone.txt"), "G", 0o600)
	if err := os.Mkdir(filepath.Join(src, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(src, "ln")); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1_600_000_000, 0)
	if err := os.Chtimes(filepath.Join(src, "a.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	return src
}

func cpMutate(t *testing.T, src string) {
	t.Helper()
	mustWrite(t, filepath.Join(src, "a.txt"), "A2-changed", 0o600)
	if err := os.Remove(filepath.Join(src, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "extra.txt"), "new", 0o644)
	mustWrite(t, filepath.Join(src, "newdir", "x"), "x", 0o644)
	if err := os.Remove(filepath.Join(src, "ln")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/b.sh", filepath.Join(src, "ln")); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointCreateAndList(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	other := filepath.Join(s.Root, "other")
	mustWrite(t, filepath.Join(other, "o.txt"), "o", 0o644)

	if out := cpOK(t, s, checkpointInput{Action: "list"}); out != "no checkpoints" {
		t.Errorf("empty list = %q", out)
	}
	out := cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	for _, want := range []string{`"base"`, src, "files: 3, dirs: 2, symlinks: 1, skipped: 0", "total: 5 bytes, newly stored: 5 bytes"} {
		if !strings.Contains(out, want) {
			t.Errorf("create output lacks %q: %s", want, out)
		}
	}
	cpOK(t, s, checkpointInput{Action: "create", Path: "other", Name: "oth"})

	all := cpOK(t, s, checkpointInput{Action: "list"})
	if !strings.Contains(all, "base  "+src+"  ") || !strings.Contains(all, "3 files  5 bytes") || !strings.Contains(all, "oth  "+other) {
		t.Errorf("list = %q", all)
	}
	filtered := cpOK(t, s, checkpointInput{Action: "list", Path: "proj"})
	if !strings.Contains(filtered, "base  ") || strings.Contains(filtered, "oth  ") {
		t.Errorf("filtered list = %q", filtered)
	}
	if out := cpOK(t, s, checkpointInput{Action: "list", Path: "nowhere"}); out != "no checkpoints" {
		t.Errorf("list of an unknown source = %q", out)
	}
}

func TestCheckpointDuplicateAndDefaultName(t *testing.T) {
	s := testSettings(t)
	cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "x"})
	if _, _, err := cpDo(t, s, checkpointInput{Action: "create", Path: "proj", Name: "x"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate name: err = %v", err)
	}

	out := cpOK(t, s, checkpointInput{Action: "create", Path: "proj"})
	m := regexp.MustCompile(`Checkpoint "(proj-\d{8}-\d{6})" created`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no default name in %q", out)
	}
	if names := cpNames(t, s); len(names) != 2 {
		t.Errorf("checkpoints = %v", names)
	}
	if err := validCheckpointName(m[1]); err != nil {
		t.Error(err)
	}
}

func TestCheckpointRestoreWithoutConfirmChangesNothing(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	cpMutate(t, src)
	before := treeState(t, src)

	out := cpOK(t, s, checkpointInput{Action: "restore", Name: "base"})
	for _, want := range []string{`"base"`, src, "3 files, 5 bytes", "pre-restore-base-", "Nothing was changed", "confirm=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q: %s", want, out)
		}
	}
	if treeState(t, src) != before {
		t.Error("the folder changed without confirm")
	}
	if names := cpNames(t, s); len(names) != 1 {
		t.Errorf("a checkpoint was created without confirm: %v", names)
	}
	if exists(filepath.Join(s.TrashDir, "files", "extra.txt")) {
		t.Error("something went to Trash without confirm")
	}
}

func TestCheckpointRestoreWithConfirm(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	want := treeState(t, src)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	cpMutate(t, src)

	out := cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
	if !strings.Contains(out, "restored: 3, moved to Trash: 2, unchanged: 3") {
		t.Errorf("summary = %q", out)
	}
	if got := treeState(t, src); got != want {
		t.Errorf("restored state differs:\n--- want\n%s--- got\n%s", want, got)
	}
	if mustRead(t, filepath.Join(src, "gone.txt")) != "G" {
		t.Error("the deleted file was not recreated")
	}
	if tgt, _ := os.Readlink(filepath.Join(src, "ln")); tgt != "a.txt" {
		t.Errorf("symlink target = %q", tgt)
	}
	fi, err := os.Stat(filepath.Join(src, "a.txt"))
	if err != nil || fi.Mode().Perm() != 0o644 || !fi.ModTime().Equal(time.Unix(1_600_000_000, 0)) {
		t.Errorf("a.txt meta = %v, %v, %v", fi, err, fi.ModTime())
	}
	if mustRead(t, filepath.Join(s.TrashDir, "files", "extra.txt")) != "new" || !exists(filepath.Join(s.TrashDir, "files", "newdir", "x")) {
		t.Error("extra files are not in Trash")
	}
	pre := cpPreRestore(t, s, "base")
	if !strings.Contains(out, "name="+pre+" confirm=true") {
		t.Errorf("undo hint missing %s: %q", pre, out)
	}
	if !strings.Contains(cpOK(t, s, checkpointInput{Action: "list"}), pre) {
		t.Error("pre-restore checkpoint is not listed")
	}
}

func TestCheckpointRestorePreRestoreRoundTrip(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	cpMutate(t, src)
	mutated := treeState(t, src)

	cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
	if treeState(t, src) == mutated {
		t.Fatal("the first restore changed nothing")
	}
	cpOK(t, s, checkpointInput{Action: "restore", Name: cpPreRestore(t, s, "base"), Confirm: true})
	if got := treeState(t, src); got != mutated {
		t.Errorf("undo differs:\n--- want\n%s--- got\n%s", mutated, got)
	}
}

func TestCheckpointRestoreRecreatesMissingFolder(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	want := treeState(t, src)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	out := cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
	if !strings.Contains(out, "no undo checkpoint") {
		t.Errorf("output = %q", out)
	}
	if got := treeState(t, src); got != want {
		t.Errorf("state differs:\n--- want\n%s--- got\n%s", want, got)
	}
}

func TestCheckpointCorruptBlobStopsRestoreBeforeChanges(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	snap, err := loadSnapshot(s.CheckpointDir, "base")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := findEntry(snap, "sub/b.sh")
	bp := blobPath(s.CheckpointDir, e.Hash)
	if err := os.Chmod(bp, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bp, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	cpMutate(t, src)
	before := treeState(t, src)

	_, _, err = cpDo(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
	if err == nil || !strings.Contains(err.Error(), "sub/b.sh") || !strings.Contains(err.Error(), e.Hash) || !strings.Contains(err.Error(), "nothing changed") {
		t.Fatalf("err = %v", err)
	}
	if treeState(t, src) != before {
		t.Error("the folder changed although a blob is corrupt")
	}
	if names := cpNames(t, s); len(names) != 1 {
		t.Errorf("a pre-restore checkpoint was created: %v", names)
	}
	if exists(filepath.Join(s.TrashDir, "files", "extra.txt")) {
		t.Error("something went to Trash")
	}
}

func TestCheckpointDeleteCleansOnlyUnusedBlobs(t *testing.T) {
	s := testSettings(t)
	src := filepath.Join(s.Root, "proj")
	mustWrite(t, filepath.Join(src, "shared.txt"), "shared", 0o644)
	mustWrite(t, filepath.Join(src, "x.txt"), "version one", 0o644)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "one"})
	mustWrite(t, filepath.Join(src, "x.txt"), "version two", 0o644)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "two"})
	if n := len(blobFiles(t, s.CheckpointDir)); n != 3 {
		t.Fatalf("blobs = %d, want 3", n)
	}

	out := cpOK(t, s, checkpointInput{Action: "delete", Name: "one"})
	if !strings.Contains(out, "unused blobs removed: 1, freed: 11 bytes") {
		t.Errorf("delete output = %q", out)
	}
	if n := len(blobFiles(t, s.CheckpointDir)); n != 2 {
		t.Errorf("blobs after delete = %d, want 2", n)
	}
	snap, _ := loadSnapshot(s.CheckpointDir, "two")
	for _, e := range snap.Entries {
		if err := verifyBlob(s.CheckpointDir, e.Hash); err != nil {
			t.Errorf("a blob of the remaining checkpoint is gone: %v", err)
		}
	}
	if _, _, err := cpDo(t, s, checkpointInput{Action: "delete", Name: "one"}); err == nil {
		t.Error("deleting a missing checkpoint succeeded")
	}
}

func TestCheckpointProtectedPaths(t *testing.T) {
	s := testSettings(t)
	if err := os.MkdirAll(s.TrashDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(s.CheckpointDir, "snapshots")
	for _, tc := range []struct{ path, why string }{
		{inside, "inside the checkpoint store"},
		{s.CheckpointDir, "protected path"},
		{s.TrashDir, "protected path"},
		{"/", "system path"},
	} {
		_, _, err := cpDo(t, s, checkpointInput{Action: "create", Path: tc.path, Name: "p"})
		if err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("create %s: err = %v, want %q", tc.path, err, tc.why)
		}
	}
	if names := cpNames(t, s); len(names) != 0 {
		t.Errorf("checkpoints were created: %v", names)
	}

	cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "ok"})
	res, err := callTool(t, deleteTool, s, deleteInput{Paths: []string{s.CheckpointDir}, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "protected") || !exists(filepath.Join(s.CheckpointDir, "snapshots", "ok.json")) {
		t.Errorf("delete of CheckpointDir: isError=%v out=%q", res.IsError, textOf(res))
	}
}

func TestCheckpointRestoreRefusesProtectedSource(t *testing.T) {
	s := testSettings(t)
	cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	snap, err := loadSnapshot(s.CheckpointDir, "base")
	if err != nil {
		t.Fatal(err)
	}
	snap.Source = s.TrashDir
	if err := writeAtomic(snapshotPath(s.CheckpointDir, "evil"), mustJSON(t, snap), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cpDo(t, s, checkpointInput{Action: "restore", Name: "evil", Confirm: true}); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckpointInvalidInput(t *testing.T) {
	s := testSettings(t)
	for _, tc := range []struct {
		in   checkpointInput
		want string
	}{
		{checkpointInput{Action: "bogus"}, "unknown action"},
		{checkpointInput{}, "unknown action"},
		{checkpointInput{Action: "create"}, "path is required"},
		{checkpointInput{Action: "restore", Confirm: true}, "name is required"},
		{checkpointInput{Action: "delete"}, "name is required"},
		{checkpointInput{Action: "delete", Name: "../x"}, "invalid checkpoint name"},
		{checkpointInput{Action: "create", Path: "p", Name: ".hidden"}, "invalid checkpoint name"},
		{checkpointInput{Action: "restore", Name: "missing", Confirm: true}, "not found"},
		{checkpointInput{Action: "create", Path: "does-not-exist", Name: "n"}, "no such file"},
	} {
		_, _, err := cpDo(t, s, tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.in, err, tc.want)
		}
	}
	notDir := filepath.Join(s.Root, "f.txt")
	mustWrite(t, notDir, "x", 0o644)
	if _, _, err := cpDo(t, s, checkpointInput{Action: "create", Path: "f.txt"}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file as source: err = %v", err)
	}
}

func TestCheckpointSecondCreateReportsDedup(t *testing.T) {
	s := testSettings(t)
	src := filepath.Join(s.Root, "big")
	for i := 0; i < 5; i++ {
		mustWrite(t, filepath.Join(src, fmt.Sprintf("f%d.bin", i)), strings.Repeat(strconv.Itoa(i), 100_000), 0o644)
	}
	mustWrite(t, filepath.Join(src, "small.txt"), "v1", 0o644)

	newly := func(out string) int {
		m := regexp.MustCompile(`newly stored: (\d+) bytes`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no newly stored in %q", out)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	first := newly(cpOK(t, s, checkpointInput{Action: "create", Path: "big", Name: "one"}))
	mustWrite(t, filepath.Join(src, "small.txt"), "v2", 0o644)
	second := newly(cpOK(t, s, checkpointInput{Action: "create", Path: "big", Name: "two"}))

	if first != 500_002 || second != 2 {
		t.Errorf("newly stored: first=%d second=%d, want 500002 and 2", first, second)
	}
	if n := len(blobFiles(t, s.CheckpointDir)); n != 7 {
		t.Errorf("blobs = %d, want 7", n)
	}
}

func TestCheckpointDiffClassifiesChanges(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	if out := cpOK(t, s, checkpointInput{Action: "diff", Name: "base"}); !strings.Contains(out, "no differences") {
		t.Errorf("fresh diff = %q", out)
	}

	cpMutate(t, src)
	if err := os.Chmod(filepath.Join(src, "sub", "b.sh"), 0o700); err != nil {
		t.Fatal(err)
	}
	before, blobs, names := treeState(t, src), len(blobFiles(t, s.CheckpointDir)), cpNames(t, s)

	out := cpOK(t, s, checkpointInput{Action: "diff", Name: "base"})
	for _, want := range []string{
		"added: 3, modified: 2, deleted: 1, meta-only: 1",
		"+ extra.txt", "+ newdir\n", "+ newdir/x", "~ a.txt", "~ ln", "- gone.txt", "= sub/b.sh",
	} {
		if !strings.Contains(out+"\n", want) {
			t.Errorf("diff lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "empty") || strings.Contains(out, "= sub\n") {
		t.Errorf("unchanged entries reported:\n%s", out)
	}
	if treeState(t, src) != before || len(blobFiles(t, s.CheckpointDir)) != blobs || len(cpNames(t, s)) != len(names) {
		t.Error("diff changed the folder, the blobs or the checkpoints")
	}
	if exists(filepath.Join(s.TrashDir, "files", "extra.txt")) {
		t.Error("diff moved something to Trash")
	}
}

func TestCheckpointDiffCapsRows(t *testing.T) {
	s := testSettings(t)
	src := filepath.Join(s.Root, "many")
	mustWrite(t, filepath.Join(src, "seed"), "s", 0o644)
	cpOK(t, s, checkpointInput{Action: "create", Path: "many", Name: "base"})
	for i := 0; i < 250; i++ {
		mustWrite(t, filepath.Join(src, fmt.Sprintf("f%03d", i)), "x", 0o644)
	}
	out := cpOK(t, s, checkpointInput{Action: "diff", Name: "base"})
	if !strings.Contains(out, "added: 250") || !strings.Contains(out, "... and 50 more") || strings.Count(out, "\n+ ") != 200 {
		t.Errorf("rows = %d, tail = %q", strings.Count(out, "\n+ "), out[len(out)-40:])
	}
}

func TestCheckpointDiffMissingSource(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	out := cpOK(t, s, checkpointInput{Action: "diff", Name: "base"})
	if !strings.Contains(out, "does not exist") || !strings.Contains(out, "added: 0, modified: 0, deleted: 6, meta-only: 0") || !strings.Contains(out, "- sub/b.sh") {
		t.Errorf("diff = %q", out)
	}
	if exists(src) {
		t.Error("diff recreated the folder")
	}
}

func TestCheckpointPartialRestoreSingleFile(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	mustWrite(t, filepath.Join(src, "a.txt"), "A-changed", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "b.sh"), "B-changed", 0o755)
	mustWrite(t, filepath.Join(src, "extra.txt"), "extra", 0o644)

	out := cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{"a.txt"}, Confirm: true})
	if !strings.Contains(out, "scope: a.txt") || !strings.Contains(out, "restored: 1, moved to Trash: 0") {
		t.Errorf("output = %q", out)
	}
	if mustRead(t, filepath.Join(src, "a.txt")) != "A1" {
		t.Error("a.txt was not restored")
	}
	if mustRead(t, filepath.Join(src, "sub", "b.sh")) != "B-changed" || mustRead(t, filepath.Join(src, "extra.txt")) != "extra" {
		t.Error("files outside the scope were touched")
	}
	if exists(filepath.Join(s.TrashDir, "files", "extra.txt")) {
		t.Error("an out-of-scope file went to Trash")
	}
	pre := cpPreRestore(t, s, "base")
	if lst := cpOK(t, s, checkpointInput{Action: "list"}); !strings.Contains(lst, pre+"  ") || !strings.Contains(lst, "[partial: 1 paths]") {
		t.Errorf("list = %q", lst)
	}
}

func TestCheckpointPartialRestoreSubfolder(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	mustWrite(t, filepath.Join(src, "sub", "b.sh"), "B-changed", 0o755)
	mustWrite(t, filepath.Join(src, "sub", "inner", "extra2.txt"), "e2", 0o644)
	mustWrite(t, filepath.Join(src, "a.txt"), "A-changed", 0o644)
	mustWrite(t, filepath.Join(src, "extra.txt"), "extra", 0o644)

	out := cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{filepath.Join(src, "sub")}, Confirm: true})
	if !strings.Contains(out, "scope: sub") || !strings.Contains(out, "moved to Trash: 1") {
		t.Errorf("output = %q", out)
	}
	if mustRead(t, filepath.Join(src, "sub", "b.sh")) != "B1" {
		t.Error("sub/b.sh was not restored")
	}
	if exists(filepath.Join(src, "sub", "inner")) || mustRead(t, filepath.Join(s.TrashDir, "files", "inner", "extra2.txt")) != "e2" {
		t.Error("the extra folder inside sub did not go to Trash")
	}
	if mustRead(t, filepath.Join(src, "a.txt")) != "A-changed" || mustRead(t, filepath.Join(src, "extra.txt")) != "extra" {
		t.Error("files outside sub were touched")
	}
}

func TestCheckpointPartialOnlyMissingInSnapshotRejected(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	cpMutate(t, src)
	before := treeState(t, src)

	_, _, err := cpDo(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{"a.txt", "extra.txt", "nope/x"}, Confirm: true})
	if err == nil || !strings.Contains(err.Error(), "extra.txt") || !strings.Contains(err.Error(), "nope/x") || !strings.HasSuffix(err.Error(), ": extra.txt, nope/x") {
		t.Fatalf("err = %v", err)
	}
	if treeState(t, src) != before || len(cpNames(t, s)) != 1 || exists(filepath.Join(s.TrashDir, "files", "extra.txt")) {
		t.Error("something changed although only has missing entries")
	}
	if _, _, err := cpDo(t, s, checkpointInput{Action: "diff", Name: "base", Only: []string{"extra.txt"}}); err == nil {
		t.Error("diff accepted an entry that is not in the checkpoint")
	}
}

func TestCheckpointPartialInvalidOnly(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	cpMutate(t, src)
	before := treeState(t, src)

	for _, only := range []string{"..", "../x", "sub/../..", ".", "./", "/", "", "  ", s.Root, filepath.Dir(src), src, filepath.Join(src, "..", "other")} {
		for _, action := range []string{"restore", "diff"} {
			_, _, err := cpDo(t, s, checkpointInput{Action: action, Name: "base", Only: []string{only}, Confirm: true})
			if err == nil || !strings.Contains(err.Error(), "only:") {
				t.Errorf("%s only=%q: err = %v", action, only, err)
			}
		}
	}
	if treeState(t, src) != before || len(cpNames(t, s)) != 1 {
		t.Error("something changed after rejected only entries")
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("f%d", i)
	}
	if _, _, err := cpDo(t, s, checkpointInput{Action: "diff", Name: "base", Only: many}); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("51 entries: err = %v", err)
	}
	if _, _, err := cpDo(t, s, checkpointInput{Action: "list", Only: []string{"a"}}); err == nil {
		t.Error("only accepted for list")
	}
}

func TestCheckpointPartialPreRestoreHoldsScopeOnly(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	mustWrite(t, filepath.Join(src, "a.txt"), "A-changed", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "extra2.txt"), "e2", 0o644)
	mustWrite(t, filepath.Join(src, "extra.txt"), "extra", 0o644)

	cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{"sub"}, Confirm: true})
	pre, err := loadSnapshot(s.CheckpointDir, cpPreRestore(t, s, "base"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pre.Partial) != 1 || pre.Partial[0] != "sub" {
		t.Errorf("Partial = %v", pre.Partial)
	}
	var paths []string
	for _, e := range pre.Entries {
		paths = append(paths, e.Path)
	}
	if got := strings.Join(paths, ","); got != "sub,sub/b.sh,sub/extra2.txt" {
		t.Errorf("pre-restore entries = %s", got)
	}
	base, _ := loadSnapshot(s.CheckpointDir, "base")
	if len(base.Partial) != 0 {
		t.Errorf("a full checkpoint got Partial = %v", base.Partial)
	}
}

func TestCheckpointPartialPreRestoreFullRestoreStaysInScope(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	mustWrite(t, filepath.Join(src, "a.txt"), "A2", 0o644)
	mustWrite(t, filepath.Join(src, "sub", "b.sh"), "B2", 0o755)

	cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{"a.txt"}, Confirm: true})
	pre := cpPreRestore(t, s, "base")
	mustWrite(t, filepath.Join(src, "late.txt"), "late", 0o644)
	afterPartial := treeState(t, src)

	if prev := cpOK(t, s, checkpointInput{Action: "restore", Name: pre}); !strings.Contains(prev, "this checkpoint is partial") || !strings.Contains(prev, "a.txt") {
		t.Errorf("preview = %q", prev)
	}
	cpOK(t, s, checkpointInput{Action: "restore", Name: pre, Confirm: true})
	if mustRead(t, filepath.Join(src, "a.txt")) != "A2" {
		t.Error("a.txt was not undone")
	}
	if mustRead(t, filepath.Join(src, "sub", "b.sh")) != "B2" || mustRead(t, filepath.Join(src, "late.txt")) != "late" {
		t.Error("the full restore of a partial checkpoint touched files outside its scope")
	}
	if exists(filepath.Join(s.TrashDir, "files", "late.txt")) {
		t.Error("an out-of-scope file went to Trash")
	}
	want := strings.Replace(afterPartial, "|A1\n", "|A2\n", 1)
	if got := treeState(t, src); got != want {
		t.Errorf("state differs:\n--- want\n%s--- got\n%s", want, got)
	}

	if _, _, err := cpDo(t, s, checkpointInput{Action: "restore", Name: pre, Only: []string{"sub/b.sh"}, Confirm: true}); err == nil || !strings.Contains(err.Error(), "outside the scope") {
		t.Errorf("only outside the partial scope: err = %v", err)
	}
	if out := cpOK(t, s, checkpointInput{Action: "diff", Name: pre}); !strings.Contains(out, "scope: a.txt") || !strings.Contains(out, "no differences") {
		t.Errorf("diff of a partial checkpoint = %q", out)
	}
}

func TestCheckpointLegacySnapshotWithoutPartialRestoresFully(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	want := treeState(t, src)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	if raw := mustRead(t, snapshotPath(s.CheckpointDir, "base")); strings.Contains(raw, `"partial"`) {
		t.Fatalf("a full snapshot must not carry a partial field: %s", raw)
	}
	cpMutate(t, src)
	cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
	if got := treeState(t, src); got != want {
		t.Errorf("state differs:\n--- want\n%s--- got\n%s", want, got)
	}
}

func TestCheckpointPartialRecreatesMissingParent(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	snap, _ := loadSnapshot(s.CheckpointDir, "base")
	subEntry, _ := findEntry(snap, "sub")
	if err := os.RemoveAll(filepath.Join(src, "sub")); err != nil {
		t.Fatal(err)
	}

	cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{"sub/b.sh"}, Confirm: true})
	fi, err := os.Stat(filepath.Join(src, "sub"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("sub was not recreated: %v", err)
	}
	if uint32(fi.Mode().Perm()) != subEntry.Mode&0o777 || fi.ModTime().UnixNano() != subEntry.ModTime {
		t.Errorf("sub meta = %v %v, want %o %d", fi.Mode().Perm(), fi.ModTime().UnixNano(), subEntry.Mode, subEntry.ModTime)
	}
	if mustRead(t, filepath.Join(src, "sub", "b.sh")) != "B1" {
		t.Error("b.sh was not restored")
	}

	if err := os.Chmod(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "sub", "b.sh"), "B-changed", 0o755)
	cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Only: []string{"sub/b.sh"}, Confirm: true})
	if fi, _ := os.Stat(filepath.Join(src, "sub")); fi.Mode().Perm() != 0o700 {
		t.Errorf("an existing parent was changed: %v", fi.Mode().Perm())
	}
}

var undoNameRe = regexp.MustCompile(`name=(\S+) confirm=true`)

func cpUndoName(t *testing.T, out string) string {
	t.Helper()
	m := undoNameRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no undo name in %q", out)
	}
	return m[1]
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestCheckpointRetentionKeepsNewestUserCheckpoints(t *testing.T) {
	s := testSettings(t)
	src := filepath.Join(s.Root, "proj")
	other := filepath.Join(s.Root, "other")
	mustWrite(t, filepath.Join(other, "o.txt"), "other", 0o644)
	cpOK(t, s, checkpointInput{Action: "create", Path: "other", Name: "o1"})

	var last string
	for i := 1; i <= 3; i++ {
		mustWrite(t, filepath.Join(src, "a.txt"), strings.Repeat("v", i), 0o644)
		last = cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: fmt.Sprintf("p%d", i)})
		if strings.Contains(last, "retention") {
			t.Fatalf("p%d triggered retention: %s", i, last)
		}
	}
	mustWrite(t, filepath.Join(src, "a.txt"), "vvvv", 0o644)
	out := cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "p4"})
	if !strings.Contains(out, "retention removed 1 old checkpoint(s): p1 (freed 1 bytes)") {
		t.Errorf("output = %q", out)
	}
	names := cpNames(t, s)
	if len(names) != 4 || hasName(names, "p1") || !hasName(names, "p4") || !hasName(names, "o1") {
		t.Errorf("checkpoints = %v", names)
	}
	if n := len(blobFiles(t, s.CheckpointDir)); n != 4 {
		t.Errorf("blobs = %d, want 4 (o.txt + 3 versions)", n)
	}
	if out := cpOK(t, s, checkpointInput{Action: "list"}); strings.Contains(out, "retention") {
		t.Errorf("list ran retention: %q", out)
	}
}

func TestCheckpointRetentionZeroMeansUnlimited(t *testing.T) {
	s := testSettings(t)
	s.CheckpointKeepMax, s.CheckpointKeepAuto = 0, 0
	src := cpProject(t, s)
	for i := 1; i <= 6; i++ {
		mustWrite(t, filepath.Join(src, "a.txt"), fmt.Sprint("v", i), 0o644)
		if out := cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: fmt.Sprint("p", i)}); strings.Contains(out, "retention") {
			t.Fatalf("output = %q", out)
		}
	}
	for i := 0; i < 4; i++ {
		mustWrite(t, filepath.Join(src, "a.txt"), "changed", 0o644)
		cpOK(t, s, checkpointInput{Action: "restore", Name: "p1", Confirm: true})
	}
	if names := cpNames(t, s); len(names) != 10 {
		t.Errorf("checkpoints = %v, want 10", names)
	}
}

func TestCheckpointRetentionLimitsAutoCheckpointsOnly(t *testing.T) {
	s := testSettings(t)
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "user2"})

	var undo []string
	var outs []string
	for i := 0; i < 4; i++ {
		mustWrite(t, filepath.Join(src, "a.txt"), fmt.Sprint("change", i), 0o644)
		out := cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
		outs = append(outs, out)
		undo = append(undo, cpUndoName(t, out))
	}
	names := cpNames(t, s)
	if hasName(names, undo[1]) || !hasName(names, undo[2]) || !hasName(names, undo[3]) {
		t.Errorf("auto checkpoints kept = %v, undo names = %v", names, undo)
	}
	if !strings.Contains(outs[3], "retention removed 1 old checkpoint(s): "+undo[1]) {
		t.Errorf("fourth restore output = %q", outs[3])
	}
	if !hasName(names, "base") || !hasName(names, "user2") || len(names) != 4 {
		t.Errorf("user checkpoints were counted or removed: %v", names)
	}
	if !strings.Contains(outs[2], "retention removed 1 old checkpoint(s): "+undo[0]) {
		t.Errorf("third restore output = %q", outs[2])
	}
	if strings.Contains(outs[1], "retention") {
		t.Errorf("second restore ran retention: %q", outs[1])
	}
	if lst := cpOK(t, s, checkpointInput{Action: "list"}); strings.Count(lst, "[auto]") != 2 {
		t.Errorf("list = %q", lst)
	}
	if sn, _ := loadSnapshot(s.CheckpointDir, undo[3]); !sn.Auto {
		t.Error("the pre-restore checkpoint is not marked Auto")
	}
	if sn, _ := loadSnapshot(s.CheckpointDir, "base"); sn.Auto {
		t.Error("a user checkpoint is marked Auto")
	}
}

func TestCheckpointRetentionUserNamedPreRestoreCountsAsUser(t *testing.T) {
	s := testSettings(t)
	src := filepath.Join(s.Root, "proj")
	for i, n := range []string{"pre-restore-a", "pre-restore-b", "pre-restore-c", "pre-restore-d"} {
		mustWrite(t, filepath.Join(src, "f.txt"), fmt.Sprint(i), 0o644)
		out := cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: n})
		if i == 3 && !strings.Contains(out, "retention removed 1 old checkpoint(s): pre-restore-a") {
			t.Errorf("output = %q", out)
		}
	}
	if names := cpNames(t, s); len(names) != 3 || hasName(names, "pre-restore-a") {
		t.Errorf("checkpoints = %v", names)
	}
	if sn, _ := loadSnapshot(s.CheckpointDir, "pre-restore-d"); sn.Auto {
		t.Error("a user checkpoint named pre-restore-* is marked Auto")
	}
	if lst := cpOK(t, s, checkpointInput{Action: "list"}); strings.Contains(lst, "[auto]") {
		t.Errorf("list = %q", lst)
	}
}

func TestCheckpointRetentionSkippedAfterPartialRestoreFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := testSettings(t)
	s.CheckpointKeepAuto = 1
	src := cpProject(t, s)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "base"})
	mustWrite(t, filepath.Join(src, "a.txt"), "changed", 0o644)
	first := cpUndoName(t, cpOK(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true}))

	if err := os.Remove(filepath.Join(src, "sub", "b.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "sub"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "sub"), 0o755) })

	out, isErr, err := cpDo(t, s, checkpointInput{Action: "restore", Name: "base", Confirm: true})
	if err != nil || !isErr || !strings.Contains(out, "failures (1)") {
		t.Fatalf("restore = %q isError=%v err=%v", out, isErr, err)
	}
	if strings.Contains(out, "retention") {
		t.Errorf("retention ran after a failed restore: %q", out)
	}
	autos := 0
	for _, n := range cpNames(t, s) {
		if strings.HasPrefix(n, "pre-restore-") {
			autos++
		}
	}
	if autos != 2 || !hasName(cpNames(t, s), first) {
		t.Errorf("auto checkpoints = %d, want both kept: %v", autos, cpNames(t, s))
	}
}

func TestCheckpointRetentionKeepsBlobsOfRemainingCheckpoints(t *testing.T) {
	s := testSettings(t)
	src := filepath.Join(s.Root, "proj")
	mustWrite(t, filepath.Join(src, "shared.txt"), "shared", 0o644)
	for i := 1; i <= 4; i++ {
		mustWrite(t, filepath.Join(src, "a.txt"), fmt.Sprint("version-", i), 0o644)
		cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: fmt.Sprint("p", i)})
	}
	if names := cpNames(t, s); hasName(names, "p1") || len(names) != 3 {
		t.Fatalf("checkpoints = %v", names)
	}
	if n := len(blobFiles(t, s.CheckpointDir)); n != 4 {
		t.Errorf("blobs = %d, want 4 (shared + versions 2-4)", n)
	}
	for _, n := range []string{"p2", "p3", "p4"} {
		snap, _ := loadSnapshot(s.CheckpointDir, n)
		for _, e := range snap.Entries {
			if err := verifyBlob(s.CheckpointDir, e.Hash); err != nil {
				t.Errorf("%s: %v", n, err)
			}
		}
	}
	out := cpOK(t, s, checkpointInput{Action: "restore", Name: "p2", Confirm: true})
	if mustRead(t, filepath.Join(src, "a.txt")) != "version-2" || mustRead(t, filepath.Join(src, "shared.txt")) != "shared" {
		t.Errorf("restore from a remaining checkpoint is wrong: %q", out)
	}
}

func TestCheckpointRetentionLegacySnapshotCountsAsUser(t *testing.T) {
	s := testSettings(t)
	s.CheckpointKeepMax = 1
	src := filepath.Join(s.Root, "proj")
	mustWrite(t, filepath.Join(src, "a.txt"), "a", 0o644)
	if _, _, err := takeSnapshot(s.CheckpointDir, src, "old"); err != nil {
		t.Fatal(err)
	}
	raw := mustRead(t, snapshotPath(s.CheckpointDir, "old"))
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "auto")
	delete(m, "created_ns")
	m["created"] = "2020-01-01T00:00:00Z"
	legacy, _ := json.Marshal(m)
	if err := os.WriteFile(snapshotPath(s.CheckpointDir, "old"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if sn, err := loadSnapshot(s.CheckpointDir, "old"); err != nil || sn.Auto {
		t.Fatalf("legacy snapshot: %+v, %v", sn.Auto, err)
	}
	if strings.Contains(string(legacy), `"auto"`) {
		t.Fatal("legacy fixture still has auto")
	}

	out := cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "new"})
	if !strings.Contains(out, "retention removed 1 old checkpoint(s): old") {
		t.Errorf("output = %q", out)
	}
	if names := cpNames(t, s); len(names) != 1 || names[0] != "new" {
		t.Errorf("checkpoints = %v", names)
	}
}

func TestCheckpointRetentionSkippedOnBrokenSnapshotJSON(t *testing.T) {
	s := testSettings(t)
	s.CheckpointKeepMax = 1
	src := filepath.Join(s.Root, "proj")
	mustWrite(t, filepath.Join(src, "a.txt"), "a", 0o644)
	cpOK(t, s, checkpointInput{Action: "create", Path: "proj", Name: "p1"})
	mustWrite(t, filepath.Join(s.CheckpointDir, "snapshots", "broken.json"), "{not json", 0o600)
	mustWrite(t, filepath.Join(src, "a.txt"), "changed", 0o644)

	out, isErr, err := cpDo(t, s, checkpointInput{Action: "create", Path: "proj", Name: "p2"})
	if err != nil || isErr {
		t.Fatalf("create failed: %q isError=%v err=%v", out, isErr, err)
	}
	if !strings.Contains(out, `Checkpoint "p2" created`) || !strings.Contains(out, "retention cleanup skipped:") || !strings.Contains(out, "broken.json") {
		t.Errorf("output = %q", out)
	}
	if !exists(snapshotPath(s.CheckpointDir, "p1")) || !exists(snapshotPath(s.CheckpointDir, "p2")) {
		t.Error("a checkpoint was removed although cleanup was skipped")
	}
}
