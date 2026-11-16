package main

// Imperative shell: persistence for shares and the session signing secret.
// Several repoview processes may run at once (same or different roots), so
// every write is a locked read-modify-write and every file is replaced
// atomically.

import (
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

// configDir is where repoview keeps its state: $REPOVIEW_CONFIG_DIR, or
// ~/.config/repoview on Linux (the OS config dir elsewhere).
func configDir() (string, error) {
	if d := os.Getenv("REPOVIEW_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "repoview"), nil
}

type shareStore interface {
	list() ([]Share, error)
	get(id string) (Share, bool, error)
	add(s Share) error
	remove(id string) (bool, error)
}

// fileShareStore keeps one JSON file per served root:
// <dir>/shares/<instanceID(root)>.json. The file is re-read on every call,
// so revocations made by another process take effect immediately.
type fileShareStore struct {
	root string
	path string
}

type shareFile struct {
	Root   string  `json:"root"`
	Shares []Share `json:"shares"`
}

func newFileShareStore(dir, root string) *fileShareStore {
	return &fileShareStore{root: root, path: filepath.Join(dir, "shares", instanceID(root)+".json")}
}

func (st *fileShareStore) read() (shareFile, error) {
	data, err := os.ReadFile(st.path)
	if errors.Is(err, fs.ErrNotExist) {
		return shareFile{Root: st.root}, nil
	}
	if err != nil {
		return shareFile{}, err
	}
	var f shareFile
	if err := json.Unmarshal(data, &f); err != nil {
		return shareFile{}, fmt.Errorf("%s is corrupt: %w", st.path, err)
	}
	if f.Root != st.root {
		return shareFile{}, fmt.Errorf("%s belongs to %q, not %q", st.path, f.Root, st.root)
	}
	return f, nil
}

func (st *fileShareStore) list() ([]Share, error) {
	f, err := st.read()
	return f.Shares, err
}

func (st *fileShareStore) get(id string) (Share, bool, error) {
	f, err := st.read()
	if err != nil {
		return Share{}, false, err
	}
	for _, s := range f.Shares {
		if constantTimeEqual(s.ID, id) {
			return s, true, nil
		}
	}
	return Share{}, false, nil
}

func (st *fileShareStore) add(s Share) error {
	return st.update(func(f *shareFile) {
		f.Shares = append(f.Shares, s)
	})
}

func (st *fileShareStore) remove(id string) (bool, error) {
	removed := false
	err := st.update(func(f *shareFile) {
		kept := f.Shares[:0]
		for _, s := range f.Shares {
			if s.ID == id {
				removed = true
				continue
			}
			kept = append(kept, s)
		}
		f.Shares = kept
	})
	return removed, err
}

// update applies fn under an exclusive cross-process lock.
func (st *fileShareStore) update(fn func(*shareFile)) error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	lock := flock.New(st.path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("lock shares: %w", err)
	}
	defer lock.Unlock()

	f, err := st.read()
	if err != nil {
		return err
	}
	fn(&f)
	if f.Shares == nil {
		f.Shares = []Share{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(st.path, append(data, '\n'), 0o600)
}

// writeFileAtomic writes to a temp file in the same directory and renames it
// into place, so readers never see a half-written file.
func writeFileAtomic(name string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil && !isWindows() {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

func isWindows() bool { return os.PathSeparator == '\\' }

// loadOrCreateSecret returns the 32-byte session signing key in dir/secret,
// creating it on first use. Concurrent first starts race on an exclusive
// create, so they all end up with the same key.
func loadOrCreateSecret(dir string) ([]byte, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, "secret")
	for attempt := 0; attempt < 3; attempt++ {
		data, err := os.ReadFile(name)
		if err == nil {
			key, err := hex.DecodeString(strings.TrimSpace(string(data)))
			if err != nil || len(key) != 32 {
				return nil, fmt.Errorf("%s is not a 64-hex-char key; delete it to regenerate", name)
			}
			return key, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Write the key elsewhere, then hard-link it into place: the link
		// fails if another process won, and readers never see a partial file.
		key := make([]byte, 32)
		if _, err := cryptoRand.Read(key); err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp(dir, ".secret.tmp-*")
		if err != nil {
			return nil, err
		}
		tmp.Chmod(0o600)
		_, werr := tmp.WriteString(hex.EncodeToString(key) + "\n")
		cerr := tmp.Close()
		if werr != nil || cerr != nil {
			os.Remove(tmp.Name())
			return nil, errors.Join(werr, cerr)
		}
		lerr := os.Link(tmp.Name(), name)
		os.Remove(tmp.Name())
		if lerr == nil {
			return key, nil
		}
		if !errors.Is(lerr, fs.ErrExist) {
			return nil, lerr
		}
		// Lost the race: loop and read the winner's key.
	}
	return nil, fmt.Errorf("could not read %s", name)
}
