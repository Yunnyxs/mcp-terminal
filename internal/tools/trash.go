package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func moveToTrash(trashDir, p string) (string, error) {

	if err := syscall.Access(filepath.Dir(p), 2); err != nil {
		return "", &fs.PathError{Op: "move to Trash", Path: p, Err: err}
	}
	filesDir, infoDir := filepath.Join(trashDir, "files"), filepath.Join(trashDir, "info")
	meta := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", (&url.URL{Path: p}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))

	base := filepath.Base(p)
	var name, infoPath, dst string
	reserved := false
	for i := 0; i < 1000 && !reserved; i++ {
		name = base
		if i > 0 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		infoPath, dst = filepath.Join(infoDir, name+".trashinfo"), filepath.Join(filesDir, name)

		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.WriteString(meta)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(infoPath)
			return "", err
		}
		if _, err := os.Lstat(dst); err == nil {
			_ = os.Remove(infoPath)
			continue
		}
		reserved = true
	}
	if !reserved {
		return "", errors.New("no free name in Trash")
	}

	if err := moveAcross(p, dst); err != nil {
		if _, statErr := os.Lstat(dst); statErr != nil {
			_ = os.Remove(infoPath)
			return "", err
		}

		return "", fmt.Errorf("%w (partial copy kept in Trash as %q; check the source)", err, name)
	}
	return name, nil
}

func moveAcross(src, dst string) error {
	err := os.Rename(src, dst)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if out, mvErr := exec.Command("mv", "--", src, dst).CombinedOutput(); mvErr != nil {
		return fmt.Errorf("mv: %v: %s", mvErr, strings.TrimSpace(string(out)))
	}
	return nil
}
