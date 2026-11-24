package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func getRecent(t *testing.T, srvURL, query string) []RecentFile {
	t.Helper()
	resp, err := http.Get(srvURL + "/api/recent" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out []RecentFile
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeAt(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestRecent_NewestFirstAcrossFolders(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	writeAt(t, filepath.Join(dir, "old.md"), base)
	writeAt(t, filepath.Join(dir, "deep", "er", "newest.md"), base.Add(3*time.Minute))
	writeAt(t, filepath.Join(dir, "mid.md"), base.Add(time.Minute))
	writeAt(t, filepath.Join(dir, ".hidden", "skip.md"), base.Add(10*time.Minute))
	setRoot(t, dir)
	srv := newTestServer(t)
	defer srv.Close()

	got := getRecent(t, srv.URL, "")
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := "deep/er/newest.md,mid.md,old.md"
	if strings.Join(paths, ",") != want {
		t.Fatalf("got %v, want %s", paths, want)
	}
	if got[0].ModTime != base.Add(3*time.Minute).UnixMilli() || got[0].Size != 1 {
		t.Errorf("metadata: %+v", got[0])
	}
}

func TestRecent_Limit(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()

	if got := getRecent(t, srv.URL, "?limit=3"); len(got) != 3 {
		t.Errorf("limit=3 returned %d", len(got))
	}
	if got := getRecent(t, srv.URL, "?limit=100000"); len(got) > recentMaxLimit {
		t.Errorf("limit not capped: %d", len(got))
	}
}

func TestRecent_EmptyIsArray(t *testing.T) {
	setRoot(t, t.TempDir())
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/recent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("got %s, want []", raw)
	}
}

// In a git repo, files the agent just created (untracked) must show up,
// while ignored files stay out.
func TestRecent_GitRepoIncludesUntrackedNotIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	now := time.Now()
	writeAt(t, filepath.Join(dir, "tracked.md"), now.Add(-time.Hour))
	// Tracked app state that changes all the time (Obsidian's workspace).
	writeAt(t, filepath.Join(dir, ".obsidian", "workspace.json"), now.Add(-time.Hour))
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0o644)
	run("add", ".")
	writeAt(t, filepath.Join(dir, ".obsidian", "workspace.json"), now.Add(time.Hour))
	writeAt(t, filepath.Join(dir, "notes", "new.md"), now.Add(time.Second))
	writeAt(t, filepath.Join(dir, "build", "out.md"), now.Add(time.Minute)) // ignored, though newest

	files := listFilesIn(dir)
	has := map[string]bool{}
	for _, f := range files {
		has[f] = true
	}
	if !has["tracked.md"] || !has["notes/new.md"] {
		t.Errorf("listing %v misses tracked or untracked files", files)
	}
	if has["build/out.md"] {
		t.Errorf("listing %v includes an ignored file", files)
	}

	setRoot(t, dir)
	srv := newTestServer(t)
	defer srv.Close()
	got := getRecent(t, srv.URL, "")
	if len(got) == 0 || got[0].Path != "notes/new.md" {
		t.Errorf("recent = %+v, want notes/new.md first", got)
	}
	for _, f := range got {
		if inDotPath(f.Path) {
			t.Errorf("recent lists dot-path %q", f.Path)
		}
	}
}

func TestRecent_ShareSeesOnlyItsFolder(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	// Make a file outside the share the newest in the root.
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(e.root, "hello.md"), future, future)
	sh := e.createShare(owner, "subdir", "pw-for-bob", 86400)

	if r := e.get(owner, "/api/recent"); !strings.Contains(r.body, `"path":"hello.md"`) {
		t.Errorf("owner recent misses hello.md: %s", r.body)
	}

	bob := e.client()
	if r := e.get(bob, sh.URL+"/api/recent"); r.code != http.StatusUnauthorized {
		t.Fatalf("locked share recent: %d", r.code)
	}
	e.unlockShare(bob, sh, "pw-for-bob")
	r := e.get(bob, sh.URL+"/api/recent")
	if r.code != http.StatusOK {
		t.Fatalf("share recent: %d", r.code)
	}
	var got []RecentFile
	json.Unmarshal([]byte(r.body), &got)
	if len(got) != 2 {
		t.Fatalf("share recent = %s, want subdir's two files", r.body)
	}
	for _, f := range got {
		if f.Path != "nested.md" && f.Path != "README.md" {
			t.Errorf("share recent leaks %q", f.Path)
		}
	}
}

func TestNewestFirst_TiesByPath(t *testing.T) {
	got := newestFirst([]RecentFile{{Path: "b", ModTime: 1}, {Path: "a", ModTime: 1}, {Path: "c", ModTime: 2}}, 10)
	if got[0].Path != "c" || got[1].Path != "a" || got[2].Path != "b" {
		t.Errorf("order %+v", got)
	}
}
