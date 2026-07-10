package storage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Layout struct{ Root, Database, Images, Temp, Trash string }

func Prepare(root string) (Layout, error) {
	l := Layout{Root: root, Database: filepath.Join(root, "database"), Images: filepath.Join(root, "images"), Temp: filepath.Join(root, "tmp"), Trash: filepath.Join(root, "trash")}
	for _, dir := range []string{l.Root, l.Database, l.Images, l.Temp, l.Trash} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return Layout{}, err
		}
	}
	if err := filepath.WalkDir(l.Temp, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".part") {
			return os.Remove(path)
		}
		return nil
	}); err != nil {
		return Layout{}, err
	}
	return l, nil
}

func FreeBytes(path string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	if s.Bavail < 0 {
		return 0, errors.New("invalid filesystem free space")
	}
	return s.Bavail * uint64(s.Bsize), nil
}
