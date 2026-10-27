package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/result"
)

const (
	maxReported = 20
	maxOnly     = 50
	maxDiffRows = 200
)

type checkpointInput struct {
	Action  string   `json:"action" jsonschema:"create | restore | list | delete"`
	Path    string   `json:"path,omitempty" jsonschema:"directory to checkpoint (create, required); optional source filter (list)"`
	Name    string   `json:"name,omitempty" jsonschema:"checkpoint name (restore and delete, required; create, optional). Letters, digits, . _ -"`
	Confirm bool     `json:"confirm,omitempty" jsonschema:"required for restore"`
	Only    []string `json:"only,omitempty" jsonschema:"restore/diff only these files or subfolders (max 50), relative to the checkpoint source or absolute inside it. Each must exist in the checkpoint"`
	Sudo    bool     `json:"sudo,omitempty" jsonschema:"run as root (sudo -n)"`
}

func (in checkpointInput) wantSudo() bool { return in.Sudo }

type checkpointPlan struct {
	Action        string   `json:"action"`
	Path          string   `json:"path"`
	Name          string   `json:"name"`
	Confirm       bool     `json:"confirm"`
	Only          []string `json:"only,omitempty"`
	KeepAuto      int      `json:"keep_auto"`
	KeepMax       int      `json:"keep_max"`
	CheckpointDir string   `json:"checkpoint_dir"`
	OwnerSet      bool     `json:"owner_set"`
	OwnerUID      int      `json:"owner_uid"`
	OwnerGID      int      `json:"owner_gid"`
	TrashDir      string   `json:"trash_dir"`
	Protected     []string `json:"protected"`
}

var checkpointTool = fileTool[checkpointInput, checkpointPlan]{
	name:    "checkpoint",
	desc:    "Snapshot a directory and restore it later. Unchanged file content is stored once. action: create | restore | list | delete | diff. diff compares a checkpoint with the folder now (read-only). restore requires confirm=true and first saves the current state as a pre-restore-* checkpoint; files that are not in the checkpoint go to Trash. only=[paths] limits restore and diff to those files or subfolders; the pre-restore checkpoint is then partial and restores only that scope.",
	timeout: deleteSudoTimeout,
	prepare: planCheckpoint,
	exec:    execCheckpoint,
}

func planCheckpoint(s config.Settings, in checkpointInput) (checkpointPlan, error) {
	plan := checkpointPlan{
		Action:        in.Action,
		Name:          in.Name,
		Confirm:       in.Confirm,
		Only:          in.Only,
		KeepAuto:      s.CheckpointKeepAuto,
		KeepMax:       s.CheckpointKeepMax,
		CheckpointDir: s.CheckpointDir,
		TrashDir:      s.TrashDir,
		Protected:     protectedPaths(s),
	}
	switch in.Action {
	case "create":
		if in.Path == "" {
			return checkpointPlan{}, errors.New("path is required for create")
		}
	case "restore", "delete", "diff":
		if in.Name == "" {
			return checkpointPlan{}, fmt.Errorf("name is required for %s", in.Action)
		}
	case "list":
	default:
		return checkpointPlan{}, fmt.Errorf("unknown action %q (use create, restore, list or delete)", in.Action)
	}
	if len(in.Only) > maxOnly {
		return checkpointPlan{}, fmt.Errorf("only has too many entries (max %d)", maxOnly)
	}
	if len(in.Only) > 0 && in.Action != "restore" && in.Action != "diff" {
		return checkpointPlan{}, errors.New("only is valid for restore and diff")
	}
	if in.Name != "" {
		if err := validCheckpointName(in.Name); err != nil {
			return checkpointPlan{}, err
		}
	}
	if in.Path != "" {
		p, err := resolve(s.Root, in.Path)
		if err != nil {
			return checkpointPlan{}, fmt.Errorf("%q: %w", in.Path, err)
		}
		plan.Path = p
	}
	if s.CheckpointDir == "" {
		return checkpointPlan{}, errors.New("checkpoint directory is not configured")
	}
	for _, sub := range []string{"blobs", "snapshots"} {
		if err := os.MkdirAll(filepath.Join(s.CheckpointDir, sub), 0o700); err != nil {
			return checkpointPlan{}, withHint(err)
		}
	}
	lock, err := os.OpenFile(filepath.Join(s.CheckpointDir, ".lock"), os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return checkpointPlan{}, withHint(err)
	}
	lock.Close()
	if fi, err := os.Stat(s.CheckpointDir); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			plan.OwnerSet, plan.OwnerUID, plan.OwnerGID = true, int(st.Uid), int(st.Gid)
		}
	}
	if in.Action == "restore" {
		for _, sub := range []string{"files", "info"} {
			if err := os.MkdirAll(filepath.Join(s.TrashDir, sub), 0o700); err != nil {
				return checkpointPlan{}, withHint(err)
			}
		}
	}
	return plan, nil
}

func lockCheckpoints(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("checkpoint lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("checkpoint lock: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func execCheckpoint(_ config.Settings, p checkpointPlan) (*mcp.CallToolResult, error) {
	unlock, err := lockCheckpoints(p.CheckpointDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer fixStoreOwner(p)

	switch p.Action {
	case "create":
		return createCheckpoint(p)
	case "list":
		return listCheckpoints(p)
	case "delete":
		return deleteCheckpoint(p)
	case "restore":
		return restoreCheckpoint(p)
	case "diff":
		return diffCheckpoint(p)
	}
	return nil, fmt.Errorf("unknown action %q", p.Action)
}

func fixStoreOwner(p checkpointPlan) {
	if !p.OwnerSet || os.Geteuid() != 0 {
		return
	}
	_ = filepath.WalkDir(p.CheckpointDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && (int(st.Uid) != p.OwnerUID || int(st.Gid) != p.OwnerGID) {
			_ = os.Lchown(path, p.OwnerUID, p.OwnerGID)
		}
		return nil
	})
}

func checkCheckpointSource(p checkpointPlan, src string) error {
	protected := make([]string, len(p.Protected))
	for i, q := range p.Protected {
		protected[i] = canon(q)
	}
	store := canon(p.CheckpointDir)
	cands := []string{canon(src)}
	if real, err := filepath.EvalSymlinks(src); err == nil && real != cands[0] {
		cands = append(cands, real)
	}
	for _, c := range cands {
		if why := protectedReason(c, protected); why != "" {
			return fmt.Errorf("%s: %s", src, why)
		}
		if c == store || strings.HasPrefix(c, store+"/") {
			return fmt.Errorf("%s: inside the checkpoint store", src)
		}
	}
	return nil
}

func defaultCheckpointName(src string, now time.Time) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, filepath.Base(src))
	base = strings.TrimLeft(base, ".")
	if len(base) > 40 {
		base = base[:40]
	}
	if base == "" {
		base = "checkpoint"
	}
	return base + "-" + now.Format("20060102-150405")
}

func freePreRestoreName(dir, name string, now time.Time) string {
	stamp := now.Format("20060102-150405")
	for i := 1; ; i++ {
		suffix := ""
		if i > 1 {
			suffix = "-" + strconv.Itoa(i)
		}
		n := name
		if max := 36 - len(suffix); len(n) > max {
			n = n[:max]
		}
		cand := "pre-restore-" + n + "-" + stamp + suffix
		if _, err := os.Lstat(snapshotPath(dir, cand)); err != nil {
			return cand
		}
	}
}

func snapSummary(snap snapshot) (files int, size int64) { return entriesSummary(snap.Entries) }

func entriesSummary(entries []snapEntry) (files int, size int64) {
	for _, e := range entries {
		if e.Kind == "file" {
			files++
			size += e.Size
		}
	}
	return files, size
}

func createCheckpoint(p checkpointPlan) (*mcp.CallToolResult, error) {
	info, err := os.Stat(p.Path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", p.Path)
	}
	if err := checkCheckpointSource(p, p.Path); err != nil {
		return nil, fmt.Errorf("create refused: %w", err)
	}
	name := p.Name
	if name == "" {
		name = defaultCheckpointName(p.Path, time.Now())
	}
	snap, st, err := takeSnapshot(p.CheckpointDir, p.Path, name)
	if err != nil {
		return nil, err
	}
	out := fmt.Sprintf("Checkpoint %q created from %s\nfiles: %d, dirs: %d, symlinks: %d, skipped: %d\ntotal: %d bytes, newly stored: %d bytes",
		snap.Name, snap.Source, st.Files, st.Dirs, st.Symlinks, st.Skipped, st.Bytes, st.NewBytes)
	out += retentionText(applyRetention(p.CheckpointDir, snap.Source, false, p.KeepMax, snap.Name))
	return result.Text(out), nil
}

func listCheckpoints(p checkpointPlan) (*mcp.CallToolResult, error) {
	snaps, err := listSnapshots(p.CheckpointDir)
	if err != nil {
		return nil, err
	}
	want := ""
	if p.Path != "" {
		want = canon(p.Path)
	}
	var lines []string
	for _, snap := range snaps {
		if want != "" && canon(snap.Source) != want {
			continue
		}
		files, size := snapSummary(snap)
		line := fmt.Sprintf("%s  %s  %s  %d files  %d bytes", snap.Name, snap.Source, snap.Created, files, size)
		if len(snap.Partial) > 0 {
			line += fmt.Sprintf("  [partial: %d paths]", len(snap.Partial))
		}
		if snap.Auto {
			line += "  [auto]"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return result.Text("no checkpoints"), nil
	}
	return result.Text(strings.Join(lines, "\n")), nil
}

func deleteCheckpoint(p checkpointPlan) (*mcp.CallToolResult, error) {
	if err := deleteSnapshot(p.CheckpointDir, p.Name); err != nil {
		return nil, err
	}
	removed, freed, err := gcBlobs(p.CheckpointDir)
	if err != nil {
		res := result.Text(fmt.Sprintf("Checkpoint %q deleted, but cleaning unused blobs failed: %v", p.Name, err))
		res.IsError = true
		return res, nil
	}
	return result.Text(fmt.Sprintf("Checkpoint %q deleted\nunused blobs removed: %d, freed: %d bytes", p.Name, removed, freed)), nil
}

type restoreRun struct {
	src                 string
	restored, unchanged int
	trashed             int
	failures            []string
}

func (r *restoreRun) fail(path string, err error) {
	r.failures = append(r.failures, fmt.Sprintf("%s: %v", path, err))
}

func normalizeOnly(source string, only []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range only {
		if strings.TrimSpace(raw) == "" {
			return nil, errors.New("only: empty entry")
		}
		abs := filepath.Clean(raw)
		if !filepath.IsAbs(raw) {
			abs = filepath.Join(source, raw)
		}
		rel, err := filepath.Rel(source, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("only: %q is outside the checkpoint source %s", raw, source)
		}
		if rel == "." {
			return nil, fmt.Errorf("only: %q is the whole source; omit only to restore everything", raw)
		}
		rel = filepath.ToSlash(rel)
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

func effectiveScope(snap snapshot, only []string) ([]string, error) {
	if len(only) == 0 {
		if len(snap.Partial) == 0 {
			return nil, nil
		}
		return append([]string{}, snap.Partial...), nil
	}
	norm, err := normalizeOnly(snap.Source, only)
	if err != nil {
		return nil, err
	}
	if len(snap.Partial) > 0 {
		var outside []string
		for _, o := range norm {
			if !inScope(o, snap.Partial) {
				outside = append(outside, o)
			}
		}
		if len(outside) > 0 {
			return nil, fmt.Errorf("only: outside the scope of partial checkpoint %q (scope: %s): %s", snap.Name, scopeText(snap.Partial), strings.Join(outside, ", "))
		}
	}
	have := make(map[string]bool, len(snap.Entries))
	for _, e := range snap.Entries {
		have[e.Path] = true
	}
	var missing []string
	for _, o := range norm {
		if !have[o] {
			missing = append(missing, o)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("only: not in checkpoint %q (nothing changed): %s", snap.Name, strings.Join(missing, ", "))
	}
	return norm, nil
}

func scopeText(scope []string) string {
	if len(scope) > 10 {
		return strings.Join(scope[:10], ", ") + fmt.Sprintf(", ... and %d more", len(scope)-10)
	}
	return strings.Join(scope, ", ")
}

func restoreCheckpoint(p checkpointPlan) (*mcp.CallToolResult, error) {
	snap, err := loadSnapshot(p.CheckpointDir, p.Name)
	if err != nil {
		return nil, err
	}
	src := snap.Source
	if err := checkCheckpointSource(p, src); err != nil {
		return nil, fmt.Errorf("restore refused: %w", err)
	}
	scope, err := effectiveScope(snap, p.Only)
	if err != nil {
		return nil, err
	}
	target := scopedEntries(snap.Entries, scope)
	now := time.Now()
	preName := freePreRestoreName(p.CheckpointDir, p.Name, now)
	files, size := entriesSummary(target)

	if !p.Confirm {
		var b strings.Builder
		fmt.Fprintf(&b, "Checkpoint %q: source %s, created %s, %d files, %d bytes.\n", snap.Name, src, snap.Created, files, size)
		if scope != nil {
			why := "only"
			if len(p.Only) == 0 {
				why = "this checkpoint is partial, so only"
			}
			fmt.Fprintf(&b, "Scope: %s these paths will be replaced: %s. Everything else in %s is left alone. Use action=diff with the same only to see what would change.\n", why, scopeText(scope), src)
			fmt.Fprintf(&b, "Restoring replaces them with the checkpoint: files below them that are not in it are moved to Trash. The current state of this scope is saved first as partial checkpoint %q.\n", preName)
		} else {
			fmt.Fprintf(&b, "Restoring will replace the contents of %s with this checkpoint: files that are not in it are moved to Trash. The current state is saved first as checkpoint %q.\n", src, preName)
		}
		b.WriteString("Nothing was changed. Repeat with confirm=true to restore.")
		return result.Text(b.String()), nil
	}

	var bad []string
	verified := map[string]bool{}
	for _, e := range target {
		if e.Kind != "file" || verified[e.Hash] {
			continue
		}
		if err := verifyBlob(p.CheckpointDir, e.Hash); err != nil {
			bad = append(bad, fmt.Sprintf("%s (hash %s): %v", e.Path, e.Hash, err))
			continue
		}
		verified[e.Hash] = true
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("restore aborted, nothing changed: %d blob(s) missing or corrupt:\n%s", len(bad), limitLines(bad))
	}

	var current []snapEntry
	undo := ""
	info, err := os.Stat(src)
	switch {
	case err == nil && !info.IsDir():
		return nil, fmt.Errorf("restore aborted, nothing changed: %s exists but is not a directory", src)
	case err == nil:
		cur, _, err := takeSnapshotFull(p.CheckpointDir, src, preName, scope, true)
		if err != nil {
			return nil, fmt.Errorf("restore aborted, nothing changed: could not save the current state: %w", err)
		}
		current, undo = cur.Entries, preName
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(src, 0o755); err != nil {
			return nil, withHint(err)
		}
	default:
		return nil, withHint(err)
	}

	all := make(map[string]snapEntry, len(snap.Entries))
	for _, e := range snap.Entries {
		all[e.Path] = e
	}
	run := &restoreRun{src: src}
	run.applyDiff(p, target, current, all)

	var b strings.Builder
	fmt.Fprintf(&b, "Restored checkpoint %q into %s\n", p.Name, src)
	if scope != nil {
		fmt.Fprintf(&b, "scope: %s\n", scopeText(scope))
	}
	fmt.Fprintf(&b, "restored: %d, moved to Trash: %d, unchanged: %d\n", run.restored, run.trashed, run.unchanged)
	if undo != "" {
		fmt.Fprintf(&b, "to undo: checkpoint action=restore name=%s confirm=true", undo)
	} else {
		b.WriteString("no undo checkpoint (the folder did not exist)")
	}
	if len(run.failures) > 0 {
		fmt.Fprintf(&b, "\nfailures (%d):\n%s", len(run.failures), limitLines(run.failures))
	}
	if len(run.failures) == 0 && undo != "" {
		b.WriteString(retentionText(applyRetention(p.CheckpointDir, src, true, p.KeepAuto, undo)))
	}
	res := result.Text(b.String())
	res.IsError = len(run.failures) > 0
	return res, nil
}

type retentionResult struct {
	removed []string
	freed   int64
	err     error
}

func applyRetention(dir, source string, auto bool, keep int, protect string) retentionResult {
	if keep <= 0 {
		return retentionResult{}
	}
	snaps, err := listSnapshots(dir)
	if err != nil {
		return retentionResult{err: err}
	}
	var group []snapshot
	for _, sn := range snaps {
		if sn.Source == source && sn.Auto == auto {
			group = append(group, sn)
		}
	}
	var res retentionResult
	if len(group) <= keep {
		return res
	}
	for _, sn := range group[keep:] {
		if sn.Name == protect {
			continue
		}
		if err := deleteSnapshot(dir, sn.Name); err != nil {
			res.err = err
			return res
		}
		res.removed = append(res.removed, sn.Name)
	}
	if len(res.removed) == 0 {
		return res
	}
	_, freed, err := gcBlobs(dir)
	res.freed, res.err = freed, err
	return res
}

func retentionText(r retentionResult) string {
	var b strings.Builder
	if len(r.removed) > 0 {
		fmt.Fprintf(&b, "\nretention removed %d old checkpoint(s): %s (freed %d bytes)", len(r.removed), strings.Join(r.removed, ", "), r.freed)
	}
	if r.err != nil {
		fmt.Fprintf(&b, "\nretention cleanup skipped: %v", r.err)
	}
	return b.String()
}

func limitLines(lines []string) string {
	if len(lines) > maxReported {
		extra := len(lines) - maxReported
		lines = append(append([]string{}, lines[:maxReported]...), fmt.Sprintf("... and %d more", extra))
	}
	return "  " + strings.Join(lines, "\n  ")
}

func (r *restoreRun) abs(rel string) string { return filepath.Join(r.src, filepath.FromSlash(rel)) }

func (r *restoreRun) applyDiff(p checkpointPlan, target, current []snapEntry, all map[string]snapEntry) {
	tgt := make(map[string]snapEntry, len(target))
	for _, e := range target {
		tgt[e.Path] = e
	}
	cur := make(map[string]snapEntry, len(current))
	for _, e := range current {
		cur[e.Path] = e
	}

	var gone []string
	for _, c := range current {
		if t, ok := tgt[c.Path]; !ok || t.Kind != c.Kind {
			gone = append(gone, c.Path)
		}
	}
	sort.Strings(gone)
	handled := map[string]bool{}
	for _, rel := range gone {
		covered := false
		for q := filepath.Dir(rel); q != "." && q != "/"; q = filepath.Dir(q) {
			if handled[q] {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		handled[rel] = true
		if _, err := moveToTrash(p.TrashDir, r.abs(rel)); err != nil {
			r.fail(rel, fmt.Errorf("move to Trash: %w", err))
			continue
		}
		r.trashed++
	}

	var dirs []snapEntry
	ancestors := map[string]bool{}
	for _, e := range target {
		for q := filepath.Dir(e.Path); q != "." && q != "/"; q = filepath.Dir(q) {
			if _, ok := tgt[q]; !ok {
				ancestors[q] = true
			}
		}
	}
	var ancList []string
	for q := range ancestors {
		ancList = append(ancList, q)
	}
	sort.Strings(ancList)
	for _, q := range ancList {
		dst := r.abs(q)
		if _, err := os.Lstat(dst); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			r.fail(q, err)
			continue
		}
		e, known := all[q]
		mode := os.FileMode(0o755)
		if known {
			mode = 0o700
		}
		if err := os.Mkdir(dst, mode); err != nil {
			r.fail(q, err)
			continue
		}
		r.restored++
		if known {
			dirs = append(dirs, e)
		}
	}

	for _, e := range target {
		if e.Kind != "dir" {
			continue
		}
		dirs = append(dirs, e)
		dst := r.abs(e.Path)
		fi, err := os.Lstat(dst)
		switch {
		case err == nil && fi.IsDir():
			r.unchanged++
		case err == nil:
			r.fail(e.Path, errors.New("exists and is not a directory"))
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(dst, 0o700); err != nil {
				r.fail(e.Path, err)
				continue
			}
			r.restored++
		default:
			r.fail(e.Path, err)
		}
	}

	for _, e := range target {
		dst := r.abs(e.Path)
		switch e.Kind {
		case "file":
			if c, ok := cur[e.Path]; ok && c.Kind == "file" && c.Hash == e.Hash {
				r.unchanged++
			} else {
				if err := restoreBlob(p.CheckpointDir, e.Hash, dst); err != nil {
					r.fail(e.Path, err)
					continue
				}
				r.restored++
			}
			if err := applyMeta(dst, e, false); err != nil {
				r.fail(e.Path, err)
			}
		case "symlink":
			if c, ok := cur[e.Path]; ok && c.Kind == "symlink" && c.Target == e.Target {
				r.unchanged++
			} else {
				if err := replaceSymlink(dst, e.Target); err != nil {
					r.fail(e.Path, err)
					continue
				}
				r.restored++
			}
			if err := applyMeta(dst, e, true); err != nil {
				r.fail(e.Path, err)
			}
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Path > dirs[j].Path })
	for _, e := range dirs {
		if err := applyMeta(r.abs(e.Path), e, false); err != nil {
			r.fail(e.Path, err)
		}
	}
}

func metaDiffers(t, c snapEntry) bool {
	if t.UID != c.UID || t.GID != c.GID {
		return true
	}
	return t.Kind != "symlink" && (t.Mode != c.Mode || t.ModTime != c.ModTime)
}

func diffCheckpoint(p checkpointPlan) (*mcp.CallToolResult, error) {
	snap, err := loadSnapshot(p.CheckpointDir, p.Name)
	if err != nil {
		return nil, err
	}
	scope, err := effectiveScope(snap, p.Only)
	if err != nil {
		return nil, err
	}
	target := scopedEntries(snap.Entries, scope)
	src := snap.Source

	missing := false
	var current []snapEntry
	switch fi, err := os.Stat(src); {
	case errors.Is(err, fs.ErrNotExist):
		missing = true
	case err != nil:
		return nil, withHint(err)
	case !fi.IsDir():
		return nil, fmt.Errorf("%s exists but is not a directory", src)
	default:
		if current, _, err = scanCurrent(p.CheckpointDir, src, scope); err != nil {
			return nil, err
		}
	}

	tgt := make(map[string]snapEntry, len(target))
	for _, e := range target {
		tgt[e.Path] = e
	}
	cur := make(map[string]snapEntry, len(current))
	for _, e := range current {
		cur[e.Path] = e
	}
	var added, modified, deleted, meta []string
	for _, c := range current {
		t, ok := tgt[c.Path]
		switch {
		case !ok:
			added = append(added, c.Path)
		case t.Kind != c.Kind, t.Kind == "file" && t.Hash != c.Hash, t.Kind == "symlink" && t.Target != c.Target:
			modified = append(modified, c.Path)
		case metaDiffers(t, c):
			meta = append(meta, c.Path)
		}
	}
	for _, t := range target {
		if _, ok := cur[t.Path]; !ok {
			deleted = append(deleted, t.Path)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Checkpoint %q vs current %s", p.Name, src)
	if scope != nil {
		fmt.Fprintf(&b, " (scope: %s)", scopeText(scope))
	}
	if missing {
		b.WriteString("\nthe source folder does not exist: every entry is reported as deleted")
	}
	if len(added)+len(modified)+len(deleted)+len(meta) == 0 {
		b.WriteString("\nno differences")
		return result.Text(b.String()), nil
	}
	fmt.Fprintf(&b, "\nadded: %d, modified: %d, deleted: %d, meta-only: %d", len(added), len(modified), len(deleted), len(meta))
	var rows []string
	for _, g := range []struct {
		mark  string
		paths []string
	}{{"+", added}, {"~", modified}, {"-", deleted}, {"=", meta}} {
		for _, path := range g.paths {
			rows = append(rows, g.mark+" "+path)
		}
	}
	if len(rows) > maxDiffRows {
		extra := len(rows) - maxDiffRows
		rows = append(rows[:maxDiffRows:maxDiffRows], fmt.Sprintf("... and %d more", extra))
	}
	b.WriteString("\n" + strings.Join(rows, "\n"))
	return result.Text(b.String()), nil
}

func replaceSymlink(dst, target string) error {
	tmp := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".tmp-link-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func applyMeta(dst string, e snapEntry, isLink bool) error {
	if err := os.Lchown(dst, e.UID, e.GID); err != nil && !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if isLink {
		return nil
	}
	if err := syscall.Chmod(dst, e.Mode); err != nil {
		return &fs.PathError{Op: "chmod", Path: dst, Err: err}
	}
	mt := time.Unix(0, e.ModTime)
	return os.Chtimes(dst, mt, mt)
}
