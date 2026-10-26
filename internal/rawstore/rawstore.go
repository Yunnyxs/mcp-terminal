package rawstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"terminal-mcp/internal/fsutil"
)

const (
	filePrefix      = "run-"
	fileSuffix      = ".txt"
	defaultKeepDays = 7
	defaultMaxFiles = 200
)

var ErrDisabled = errors.New("rawstore: storage directory not set")

type Store struct {
	dir      string
	keepFor  time.Duration
	maxFiles int
	now      func() time.Time
}

func New(dir string) *Store {
	if dir == "" {
		return nil
	}
	return &Store{dir: dir, keepFor: defaultKeepDays * 24 * time.Hour, maxFiles: defaultMaxFiles, now: time.Now}
}

func (s *Store) Save(data []byte) (string, error) {
	if s == nil {
		return "", ErrDisabled
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	s.prune()
	var salt [3]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	name := filePrefix + s.now().Format("20060102-150405") + "-" + hex.EncodeToString(salt[:]) + fileSuffix
	path := filepath.Join(s.dir, name)
	if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	cutoff := s.now().Add(-s.keepFor)
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(s.dir, name)
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
			continue
		}
		files = append(files, file{p, info.ModTime()})
	}
	if over := len(files) - s.maxFiles + 1; over > 0 {
		sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
		for _, f := range files[:over] {
			_ = os.Remove(f.path)
		}
	}
}
