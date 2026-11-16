package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func (e *authEnv) loginFrom(c *http.Client, ip, pw string) int {
	return e.do(c, "POST", "/login", strings.NewReader("password="+url.QueryEscape(pw)), map[string]string{
		"Content-Type":     "application/x-www-form-urlencoded",
		"Cf-Connecting-IP": ip,
	}).code
}

// An attacker exhausting the owner login from many IPs can't lock the owner
// out of devices they've signed in on before.
func TestOwnerLogin_KnownDeviceSurvivesTargetLockout(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	if code := e.loginFrom(owner, "192.0.2.1", ownerPW); code != http.StatusSeeOther {
		t.Fatalf("first login: %d", code)
	}
	e.postForm(owner, "/logout", nil)

	attacker := e.client()
	for i := 0; i < loginTargetMax+5; i++ {
		e.loginFrom(attacker, fmt.Sprintf("198.51.%d.%d", i/250, i%250), "guess")
	}
	if code := e.loginFrom(e.client(), "203.0.113.7", ownerPW); code != http.StatusTooManyRequests {
		t.Fatalf("unknown device during the attack: %d, want 429", code)
	}
	if code := e.loginFrom(owner, "203.0.113.8", ownerPW); code != http.StatusSeeOther {
		t.Fatalf("known device during the attack: %d, want 303", code)
	}
}

// A device cookie is not a session and only works for its own instance.
func TestDeviceCookie_GrantsNothingElse(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	e.postForm(owner, "/logout", nil)
	if r := e.get(owner, "/api/tree"); r.code != http.StatusUnauthorized {
		t.Fatalf("device cookie must not authenticate: %d", r.code)
	}
	tok := signToken(e.app.secret, claims{Kind: kindDevice, Inst: e.app.inst, Exp: e.now().Unix() + 3600})
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/tree", nil)
	req.AddCookie(&http.Cookie{Name: ownerCookieName(e.app.inst), Value: tok})
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("device token presented as a session: %d", r.StatusCode)
	}
}

// Flooding the owner login must not block share logins (separate limiters).
func TestShareLogin_UnaffectedByOwnerLoginFlood(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "pw", 0)

	c := e.client()
	for i := 0; i < limiterMaxEntries+loginTargetMax; i++ {
		e.loginFrom(c, fmt.Sprintf("2001:db8:%x:%x::1", i/65536, i%65536), "guess")
	}
	r := e.do(e.client(), "POST", sh.URL+"/login", strings.NewReader("password=pw"), map[string]string{
		"Content-Type":     "application/x-www-form-urlencoded",
		"Cf-Connecting-IP": "203.0.113.99",
	})
	if r.code != http.StatusSeeOther {
		t.Fatalf("share login after owner flood: %d", r.code)
	}
}

func TestLogin_BodyIsBounded(t *testing.T) {
	e := newAuthEnv(t)
	big := "password=" + strings.Repeat("x", 1<<20)
	r := e.do(e.client(), "POST", "/login", strings.NewReader(big), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       e.srv.URL,
	})
	if r.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("1 MB login body: %d", r.code)
	}
	r = e.do(e.client(), "POST", "/login", strings.NewReader(big), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       "https://evil.example",
	})
	if r.code != http.StatusForbidden {
		t.Fatalf("cross-site big body: %d", r.code)
	}
}

func TestSharePrompt_DoesNotRevealName(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	os.MkdirAll(filepath.Join(e.root, "Q3 board deck"), 0o755)
	sh := e.createShare(owner, "Q3 board deck", "pw", 0)
	r := e.get(e.client(), sh.URL+"/")
	if strings.Contains(r.body, "Q3 board deck") || !strings.Contains(r.body, "Protected link") {
		t.Fatalf("prompt reveals the item name or lacks the generic heading: %s", r.body)
	}
}

// One-click links carry the share password after "#k=". Only the share
// prompt reads it (browsers never send the fragment to the server).
func TestSharePrompt_ReadsKeyFromFragment(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "pw", 0)
	prompt := e.get(e.client(), sh.URL+"/")
	if !strings.Contains(prompt.body, `id="rv-key-unlock"`) {
		t.Fatalf("share prompt lacks the one-click unlock script: %s", prompt.body)
	}
	wrong := e.postForm(e.client(), sh.URL+"/login", url.Values{"password": {"nope"}})
	if wrong.code != http.StatusUnauthorized || strings.Contains(wrong.body, `id="rv-key-unlock"`) {
		t.Fatalf("the incorrect-password page must not auto-submit again (code %d)", wrong.code)
	}
	login := e.get(e.client(), "/login")
	if strings.Contains(login.body, `id="rv-key-unlock"`) {
		t.Fatal("owner login page must not read keys from the fragment")
	}
}

func TestShareArea_UncleanPathsAre404NotRedirects(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "", 0)
	for _, p := range []string{"/_raw/deep/..%2F.hidden", "/api/x/..%2Ftree", "/_raw/a/./b"} {
		r := e.get(e.client(), sh.URL+p)
		if loc := r.header.Get("Location"); r.code >= 300 && r.code < 400 && !strings.HasPrefix(loc, sh.URL+"/") {
			t.Errorf("%s redirected out of the share to %q", p, loc)
		}
	}
}

func TestLoadOrCreateSecret_TightensExistingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	os.MkdirAll(dir, 0o755)
	if _, err := loadOrCreateSecret(dir); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode %v, want 0700", info.Mode().Perm())
	}
}

// Search and backlinks read every file; only a few may run at once, and a
// waiting request gives up when its client does.
func TestHeavyEndpoints_ConcurrencyBounded(t *testing.T) {
	setRoot(t, "testdata")
	for i := 0; i < cap(heavySlots); i++ {
		heavySlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(heavySlots); i++ {
			<-heavySlots
		}
	}()
	for _, u := range []string{"/api/search?q=hello", "/api/backlinks?path=hello.md", "/api/jsonl?path=small.jsonl"} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		r := httptest.NewRequest("GET", u, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		apiHandler().ServeHTTP(w, r)
		cancel()
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s with all slots busy: %d, want 503", u, w.Code)
		}
	}
}

func TestHTTPServer_HasTimeouts(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout == 0 || srv.IdleTimeout == 0 || srv.MaxHeaderBytes == 0 {
		t.Fatalf("server limits not set: %+v", srv)
	}
}
