package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

type snapEntry struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Hash    string `json:"hash,omitempty"`
	Target  string `json:"target,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Mode    uint32 `json:"mode"`
	ModTime int64  `json:"mtime"`
	UID     int    `json:"uid"`
	GID     int    `json:"gid"`
}

type snapshot struct {
	Name      string      `json:"name"`
	Source    string      `json:"source"`
	Created   string      `json:"created"`
	Entries   []snapEntry `json:"entries"`
	Partial   []string    `json:"partial,omitempty"`
	Auto      bool        `json:"auto,omitempty"`
	CreatedNS int64       `json:"created_ns,omitempty"`
}

type scanStats struct {
	Files, Dirs, Symlinks, Skipped int
	Bytes, NewBytes                int64
}

const (
	blobsDirName     = "blobs"
	snapshotsDirName = "snapshots"
	snapshotExt      = ".json"
)

var (
	checkpointNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	blobHashRe       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func validCheckpointName(name string) error {
	if !checkpointNameRe.MatchString(name) {
		return fmt.Errorf("invalid checkpoint name %q: use 1-64 letters, digits, '.', '_' or '-'", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid checkpoint name %q: must not start with '.'", name)
	}
	return nil
}

func validBlobHash(hash string) error {
	if !blobHashRe.MatchString(hash) {
		return fmt.Errorf("invalid blob hash %q", hash)
	}
	return nil
}

func blobPath(dir, hash string) string {
	return filepath.Join(dir, blobsDirName, hash[:2], hash)
}

func snapshotPath(dir, name string) string {
	return filepath.Join(dir, snapshotsDirName, name+snapshotExt)
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func storeBlob(dir, src string) (hash string, size int64, isNew bool, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, false, err
	}
	defer in.Close()

	blobs := filepath.Join(dir, blobsDirName)
	tmp, err := os.CreateTemp(blobs, ".tmp-*")
	if err != nil {
		return "", 0, false, err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), in)
	if err != nil {
		cleanup()
		return "", 0, false, err
	}
	hash = hex.EncodeToString(h.Sum(nil))
	final := blobPath(dir, hash)

	if _, err := os.Lstat(final); err == nil {
		cleanup()
		return hash, size, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		cleanup()
		return "", 0, false, err
	}
	if err := tmp.Chmod(0o400); err != nil {
		cleanup()
		return "", 0, false, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", 0, false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", 0, false, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		os.Remove(tmpName)
		return "", 0, false, err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return "", 0, false, err
	}
	return hash, size, true, nil
}

func entryMeta(info fs.FileInfo) (mode uint32, uid, gid int) {
	mode = uint32(info.Mode().Perm())
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		mode = st.Mode & 0o7777
		uid, gid = int(st.Uid), int(st.Gid)
	}
	return mode, uid, gid
}

func inScope(rel string, scope []string) bool {
	for _, s := range scope {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

func aboveScope(rel string, scope []string) bool {
	for _, s := range scope {
		if strings.HasPrefix(s, rel+"/") {
			return true
		}
	}
	return false
}

func scopedEntries(entries []snapEntry, scope []string) []snapEntry {
	if scope == nil {
		return entries
	}
	var out []snapEntry
	for _, e := range entries {
		if inScope(e.Path, scope) {
			out = append(out, e)
		}
	}
	return out
}

func scanTree(dir, root, skipDir string, scope []string, store bool) ([]snapEntry, scanStats, error) {
	var stats scanStats
	var entries []snapEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return fmt.Errorf("%s: %w", path, werr)
		}
		if path == root {
			return nil
		}
		if skipDir != "" && path == skipDir {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		slashRel := filepath.ToSlash(rel)
		if scope != nil && !inScope(slashRel, scope) {
			if d.IsDir() && !aboveScope(slashRel, scope) {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		mode, uid, gid := entryMeta(fi)
		e := snapEntry{
			Path:    slashRel,
			Mode:    mode,
			ModTime: fi.ModTime().UnixNano(),
			UID:     uid,
			GID:     gid,
		}
		switch t := fi.Mode().Type(); {
		case t == 0:
			var hash string
			var size int64
			isNew := false
			if store {
				hash, size, isNew, err = storeBlob(dir, path)
			} else {
				size = fi.Size()
				hash, err = hashFile(path)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			e.Kind, e.Hash, e.Size = "file", hash, size
			stats.Files++
			stats.Bytes += size
			if isNew {
				stats.NewBytes += size
			}
		case t&fs.ModeDir != 0:
			e.Kind = "dir"
			stats.Dirs++
		case t&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			e.Kind, e.Target = "symlink", target
			stats.Symlinks++
		default:
			stats.Skipped++
			return nil
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, scanStats{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, stats, nil
}

func resolveRoot(source string) (src, walkRoot string, err error) {
	src, err = filepath.Abs(source)
	if err != nil {
		return "", "", fmt.Errorf("resolve source: %w", err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", "", fmt.Errorf("source: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("source is not a directory: %s", src)
	}
	walkRoot, err = filepath.EvalSymlinks(src)
	if err != nil {
		return "", "", fmt.Errorf("resolve source: %w", err)
	}
	return src, walkRoot, nil
}

func scanCurrent(dir, source string, scope []string) ([]snapEntry, scanStats, error) {
	src, walkRoot, err := resolveRoot(source)
	if err != nil {
		return nil, scanStats{}, err
	}
	skipDir := ""
	if abs, err := filepath.Abs(dir); err == nil {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			skipDir = real
		}
	}
	entries, stats, err := scanTree(dir, walkRoot, skipDir, scope, false)
	if err != nil {
		return nil, scanStats{}, fmt.Errorf("scan %s: %w", src, err)
	}
	return entries, stats, nil
}

func takeSnapshot(dir, source, name string) (snapshot, scanStats, error) {
	return takeSnapshotScoped(dir, source, name, nil)
}

func takeSnapshotScoped(dir, source, name string, scope []string) (snapshot, scanStats, error) {
	return takeSnapshotFull(dir, source, name, scope, false)
}

func takeSnapshotFull(dir, source, name string, scope []string, auto bool) (snapshot, scanStats, error) {
	var stats scanStats
	if err := validCheckpointName(name); err != nil {
		return snapshot{}, stats, err
	}
	src, walkRoot, err := resolveRoot(source)
	if err != nil {
		return snapshot{}, stats, err
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return snapshot{}, stats, fmt.Errorf("resolve checkpoint dir: %w", err)
	}
	for _, d := range []string{filepath.Join(absDir, blobsDirName), filepath.Join(absDir, snapshotsDirName)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return snapshot{}, stats, fmt.Errorf("create checkpoint dir: %w", err)
		}
	}
	dir = absDir
	if _, err := os.Lstat(snapshotPath(dir, name)); err == nil {
		return snapshot{}, stats, fmt.Errorf("checkpoint %q already exists", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return snapshot{}, stats, fmt.Errorf("check checkpoint %q: %w", name, err)
	}
	skipDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return snapshot{}, stats, fmt.Errorf("resolve checkpoint dir: %w", err)
	}

	entries, stats, err := scanTree(dir, walkRoot, skipDir, scope, true)
	if err != nil {
		return snapshot{}, scanStats{}, fmt.Errorf("scan %s: %w", src, err)
	}

	now := time.Now().UTC()
	snap := snapshot{
		Name:      name,
		Source:    src,
		Created:   now.Format(time.RFC3339),
		CreatedNS: now.UnixNano(),
		Entries:   entries,
		Auto:      auto,
	}
	if scope != nil {
		snap.Partial = append([]string{}, scope...)
	}
	if snap.Entries == nil {
		snap.Entries = []snapEntry{}
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return snapshot{}, scanStats{}, fmt.Errorf("encode snapshot: %w", err)
	}
	if _, err := os.Lstat(snapshotPath(dir, name)); err == nil {
		return snapshot{}, scanStats{}, fmt.Errorf("checkpoint %q already exists", name)
	}
	if err := writeAtomic(snapshotPath(dir, name), data, 0o600); err != nil {
		return snapshot{}, scanStats{}, fmt.Errorf("write snapshot: %w", err)
	}
	return snap, stats, nil
}

func readSnapshotFile(path string) (snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return snapshot{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return snap, nil
}

func loadSnapshot(dir, name string) (snapshot, error) {
	if err := validCheckpointName(name); err != nil {
		return snapshot{}, err
	}
	snap, err := readSnapshotFile(snapshotPath(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return snapshot{}, fmt.Errorf("checkpoint %q not found", name)
	}
	if err != nil {
		return snapshot{}, fmt.Errorf("load checkpoint %q: %w", name, err)
	}
	return snap, nil
}

func listSnapshots(dir string) ([]snapshot, error) {
	files, err := os.ReadDir(filepath.Join(dir, snapshotsDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	var out []snapshot
	for _, f := range files {
		name, ok := strings.CutSuffix(f.Name(), snapshotExt)
		if !ok || f.IsDir() || validCheckpointName(name) != nil {
			continue
		}
		snap, err := loadSnapshot(dir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool {
		if ti, tj := snapTime(out[i]), snapTime(out[j]); ti != tj {
			return ti > tj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func snapTime(s snapshot) int64 {
	if s.CreatedNS != 0 {
		return s.CreatedNS
	}
	if t, err := time.Parse(time.RFC3339, s.Created); err == nil {
		return t.UnixNano()
	}
	return 0
}

func deleteSnapshot(dir, name string) error {
	if err := validCheckpointName(name); err != nil {
		return err
	}
	if err := os.Remove(snapshotPath(dir, name)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checkpoint %q not found", name)
		}
		return fmt.Errorf("delete checkpoint %q: %w", name, err)
	}
	return nil
}

func gcBlobs(dir string) (removed int, freed int64, err error) {
	files, err := os.ReadDir(filepath.Join(dir, snapshotsDirName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, 0, fmt.Errorf("gc: %w", err)
	}
	used := map[string]bool{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), snapshotExt) {
			continue
		}
		snap, err := readSnapshotFile(filepath.Join(dir, snapshotsDirName, f.Name()))
		if err != nil {
			return 0, 0, fmt.Errorf("gc aborted, nothing removed: %w", err)
		}
		for _, e := range snap.Entries {
			if e.Kind == "file" {
				used[e.Hash] = true
			}
		}
	}

	blobs := filepath.Join(dir, blobsDirName)
	prefixes, err := os.ReadDir(blobs)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("gc: %w", err)
	}
	for _, p := range prefixes {
		if !p.IsDir() || len(p.Name()) != 2 {
			continue
		}
		sub := filepath.Join(blobs, p.Name())
		items, err := os.ReadDir(sub)
		if err != nil {
			return removed, freed, fmt.Errorf("gc: %w", err)
		}
		left := len(items)
		for _, it := range items {
			if it.IsDir() || !blobHashRe.MatchString(it.Name()) || it.Name()[:2] != p.Name() || used[it.Name()] {
				continue
			}
			info, err := it.Info()
			if err != nil {
				return removed, freed, fmt.Errorf("gc: %w", err)
			}
			if err := os.Remove(filepath.Join(sub, it.Name())); err != nil {
				return removed, freed, fmt.Errorf("gc: %w", err)
			}
			removed++
			freed += info.Size()
			left--
		}
		if left == 0 {
			os.Remove(sub)
		}
	}
	return removed, freed, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyBlob(dir, hash string) error {
	if err := validBlobHash(hash); err != nil {
		return err
	}
	got, err := hashFile(blobPath(dir, hash))
	if err != nil {
		return fmt.Errorf("blob %s: %w", hash, err)
	}
	if got != hash {
		return fmt.Errorf("blob %s is corrupt (content hashes to %s)", hash, got)
	}
	return nil
}

func restoreBlob(dir, hash, dst string) error {
	if err := validBlobHash(hash); err != nil {
		return err
	}
	in, err := os.Open(blobPath(dir, hash))
	if err != nil {
		return fmt.Errorf("blob %s: %w", hash, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return fmt.Errorf("restore %s: %w", dst, err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		return fail(fmt.Errorf("restore %s: %w", dst, err))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != hash {
		return fail(fmt.Errorf("blob %s is corrupt (content hashes to %s)", hash, got))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("restore %s: %w", dst, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("restore %s: %w", dst, err)
	}
	return nil
}
