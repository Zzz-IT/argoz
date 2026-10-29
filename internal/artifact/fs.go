package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func sanitizeSegment(s, fallback string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '-', r == '_', r == '+':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if len(out) > 96 {
		out = out[:96]
	}
	if out == "" || out == "." || out == ".." {
		return fallback
	}
	return out
}

func fsyncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replacePath(tmpName, path); err != nil {
		return err
	}
	fsyncDir(filepath.Dir(path))
	return nil
}

// copyFileAtomic writes src to a temporary file in target's directory, fsyncs
// it, then renames it over target. It returns the staged file's SHA-256.
func copyFileAtomic(src, target string, perm os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".new-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", err
	}
	fsyncDir(filepath.Dir(target))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func uniqueBackupName(dir, base string) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102T150405.000000000Z"), sanitizeSegment(base, "artifact")))
}
