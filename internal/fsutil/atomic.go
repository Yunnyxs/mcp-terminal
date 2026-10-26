package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	return writeAtomic(path, data, mode, nil)
}

func WriteFileLike(path string, data []byte, like fs.FileInfo) error {
	mode := like.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
	var own *owner
	if st, ok := like.Sys().(*syscall.Stat_t); ok {
		own = &owner{uid: int(st.Uid), gid: int(st.Gid)}
	}
	return writeAtomic(path, data, mode, own)
}

type owner struct{ uid, gid int }

func writeAtomic(path string, data []byte, mode fs.FileMode, own *owner) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if own != nil {
		_ = tmp.Chown(own.uid, own.gid)
	}
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
