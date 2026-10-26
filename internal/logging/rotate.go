package logging

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const segments = 20

const stampLayout = "20060102-150405.000000"

type Rotating struct {
	mu      sync.Mutex
	path    string
	max     int64
	segSize int64
	f       *os.File
	size    int64
}

func Open(path string, maxBytes int64) (*Rotating, error) {

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &Rotating{path: path, max: maxBytes, segSize: max(1, maxBytes/segments)}
	if err := r.openActive(); err != nil {
		return nil, err
	}
	r.prune()
	return r, nil
}

func (r *Rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size > 0 && r.size+int64(len(p)) > r.segSize {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *Rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

func (r *Rotating) openActive() error {
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, info.Size()
	return nil
}

func (r *Rotating) rotate() error {
	_ = r.f.Close()
	if err := os.Rename(r.path, r.path+"."+time.Now().Format(stampLayout)); err != nil {
		_ = r.openActive()
		return err
	}
	if err := r.openActive(); err != nil {
		return err
	}
	r.prune()
	return nil
}

func (r *Rotating) prune() {
	dir, prefix := filepath.Dir(r.path), filepath.Base(r.path)+"."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type segment struct {
		name string
		size int64
	}
	var old []segment
	total := r.size
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if _, err := time.Parse(stampLayout, name[len(prefix):]); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		old = append(old, segment{name, info.Size()})
		total += info.Size()
	}
	sort.Slice(old, func(i, j int) bool { return old[i].name < old[j].name })
	for _, s := range old {
		if total <= r.max-r.segSize {
			break
		}
		if os.Remove(filepath.Join(dir, s.name)) == nil {
			total -= s.size
		}
	}
}
