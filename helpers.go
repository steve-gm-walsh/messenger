package messenger

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// NewCode returns a random pairing code of n bytes as lowercase hex.
func NewCode(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // unreachable: since Go 1.24 crypto/rand.Read crashes rather than fail
	}
	return hex.EncodeToString(b)
}

// Command splits "/name@bot args" into ("name", "args"), lowercasing the
// name and dropping any @bot suffix. Text that does not start with "/"
// returns ("", text trimmed).
func Command(text string) (name, args string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", text
	}
	name, args, _ = strings.Cut(text[1:], " ")
	name, _, _ = strings.Cut(name, "@")
	return strings.ToLower(name), strings.TrimSpace(args)
}

// WriteFileAtomic replaces path with data, mode 0600: a temp file in the
// same directory is synced and renamed over path, so a crash leaves the
// old file or the new one, never half of each. It is for secrets files.
func WriteFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := errors.Join(fill(f, data), f.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func fill(f *os.File, data []byte) error {
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}
