package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func testShare(t *testing.T, p string) Share {
	t.Helper()
	s, err := newShare(bytes.NewReader(randomBytes(t, 256)), time.Now(), p, false, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := cryptoRand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestShareStore_CRUD(t *testing.T) {
	st := newFileShareStore(t.TempDir(), "/srv/repo")

	if got, err := st.list(); err != nil || len(got) != 0 {
		t.Fatalf("empty list = %v, %v", got, err)
	}
	a, b := testShare(t, "a.md"), testShare(t, "b.md")
	for _, s := range []Share{a, b} {
		if err := st.add(s); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := st.get(a.ID)
	if err != nil || !ok || got.Path != "a.md" {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}
	if _, ok, _ := st.get("missing"); ok {
		t.Fatal("missing id must not be found")
	}
	if removed, err := st.remove(a.ID); err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if removed, _ := st.remove(a.ID); removed {
		t.Fatal("second remove must report nothing removed")
	}
	list, _ := st.list()
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestShareStore_IsolatedPerRoot(t *testing.T) {
	dir := t.TempDir()
	one := newFileShareStore(dir, "/srv/one")
	two := newFileShareStore(dir, "/srv/two")
	s := testShare(t, "x.md")
	if err := one.add(s); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := two.get(s.ID); ok {
		t.Fatal("a share for one root must not be visible to another root")
	}
}

// Two repoview processes serving the same root use separate store values
// over the same file; concurrent writes must not lose shares.
func TestShareStore_ConcurrentWritersDontLoseShares(t *testing.T) {
	dir := t.TempDir()
	const writers, each = 4, 10
	var wg sync.WaitGroup
	ids := make(chan string, writers*each)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := newFileShareStore(dir, "/srv/repo") // its own "process"
			for i := 0; i < each; i++ {
				s := testShare(t, "f.md")
				if err := st.add(s); err != nil {
					t.Error(err)
					return
				}
				ids <- s.ID
			}
		}()
	}
	wg.Wait()
	close(ids)

	list, err := newFileShareStore(dir, "/srv/repo").list()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range list {
		have[s.ID] = true
	}
	for id := range ids {
		if !have[id] {
			t.Errorf("share %s lost", id)
		}
	}
	if len(list) != writers*each {
		t.Fatalf("got %d shares, want %d", len(list), writers*each)
	}
}

func TestShareStore_SeesOtherProcessWrites(t *testing.T) {
	dir := t.TempDir()
	a := newFileShareStore(dir, "/srv/repo")
	b := newFileShareStore(dir, "/srv/repo")
	s := testShare(t, "x.md")
	if err := a.add(s); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.get(s.ID); !ok {
		t.Fatal("b must see a's share")
	}
	if _, err := b.remove(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := a.get(s.ID); ok {
		t.Fatal("revocation by b must take effect in a immediately")
	}
}

func TestShareStore_PrivateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	st := newFileShareStore(dir, "/srv/repo")
	if err := st.add(testShare(t, "x.md")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("share file mode %v, want 0600", info.Mode().Perm())
	}
}

func TestShareStore_CorruptFileIsAnErrorNotAWipe(t *testing.T) {
	dir := t.TempDir()
	st := newFileShareStore(dir, "/srv/repo")
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.list(); err == nil {
		t.Fatal("corrupt file must surface an error")
	}
	if err := st.add(testShare(t, "x.md")); err == nil {
		t.Fatal("must not overwrite a corrupt file")
	}
	if data, _ := os.ReadFile(st.path); string(data) != "{not json" {
		t.Fatal("corrupt file was modified")
	}
}

// ── secret ──────────────────────────────────────────────────

func TestLoadOrCreateSecret(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	got := make([][]byte, 8)
	errs := make([]error, 8)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = loadOrCreateSecret(dir)
		}(i)
	}
	wg.Wait()
	for i := range got {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if len(got[i]) != 32 || !bytes.Equal(got[i], got[0]) {
			t.Fatalf("concurrent starts must agree on one 32-byte secret")
		}
	}
	again, err := loadOrCreateSecret(dir)
	if err != nil || !bytes.Equal(again, got[0]) {
		t.Fatal("secret must persist")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, "secret"))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("secret mode %v", info.Mode().Perm())
		}
	}
}

func TestLoadOrCreateSecret_RejectsShortFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secret"), []byte("abcd"), 0o600)
	if _, err := loadOrCreateSecret(dir); err == nil {
		t.Fatal("a truncated secret must be an error")
	}
}
