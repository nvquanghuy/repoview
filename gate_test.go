package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── harness ─────────────────────────────────────────────────

const ownerPW = "correct horse"

type authEnv struct {
	t      *testing.T
	srv    *httptest.Server
	app    *server
	root   string
	cfgDir string
	mu     sync.Mutex
	clock  time.Time
}

func (e *authEnv) now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

func (e *authEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.clock = e.clock.Add(d)
	e.mu.Unlock()
}

// newAuthEnv serves a copy of testdata with an owner password configured.
func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	root := copyTestdata(t)
	cfgDir := t.TempDir()
	e := &authEnv{t: t, root: root, cfgDir: cfgDir, clock: time.Unix(1_800_000_000, 0)}
	e.app = mustAuthServer(t, root, cfgDir, ownerPW, e.now)
	e.srv = httptest.NewServer(e.app.handler())
	t.Cleanup(e.srv.Close)
	return e
}

func mustAuthServer(t *testing.T, root, cfgDir, pw string, now func() time.Time) *server {
	t.Helper()
	secret, err := loadOrCreateSecret(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := newOwnerCredential(secret, pw, "")
	if err != nil {
		t.Fatal(err)
	}
	app := newServer(serverConfig{
		root:   root,
		owner:  &cred,
		secret: secret,
		shares: newFileShareStore(cfgDir, root),
		now:    now,
	})
	return app
}

func copyTestdata(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "repo")
	src, _ := filepath.Abs("testdata")
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// client returns a browser-like client: cookie jar, no redirect following.
func (e *authEnv) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type resp struct {
	code   int
	body   string
	header http.Header
}

func (e *authEnv) do(c *http.Client, method, path string, body io.Reader, hdr map[string]string) resp {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, string(b), r.Header}
}

func (e *authEnv) get(c *http.Client, path string) resp { return e.do(c, "GET", path, nil, nil) }

func (e *authEnv) postForm(c *http.Client, path string, form url.Values) resp {
	return e.do(c, "POST", path, strings.NewReader(form.Encode()), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       e.srv.URL,
	})
}

func (e *authEnv) login(c *http.Client) {
	e.t.Helper()
	if r := e.postForm(c, "/login", url.Values{"password": {ownerPW}, "next": {"/"}}); r.code != http.StatusSeeOther {
		e.t.Fatalf("login: %d %s", r.code, r.body)
	}
}

func (e *authEnv) createShare(c *http.Client, path, password string, expiresIn int) shareView {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"path": path, "password": password, "expiresIn": expiresIn})
	r := e.do(c, "POST", "/api/shares", strings.NewReader(string(body)), map[string]string{
		"Content-Type": "application/json",
		"Origin":       e.srv.URL,
	})
	if r.code != http.StatusCreated {
		e.t.Fatalf("create share: %d %s", r.code, r.body)
	}
	var v shareView
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *authEnv) unlockShare(c *http.Client, sh shareView, pw string) resp {
	return e.postForm(c, sh.URL+"/login", url.Values{"password": {pw}})
}

func treeNamesFromBody(t *testing.T, body string) map[string]bool {
	t.Helper()
	var entries []TreeEntry
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("tree JSON: %v (%s)", err, body)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	return names
}

// ── local mode (no password) ────────────────────────────────

func TestLocalMode_Unchanged(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()
	for path, want := range map[string]int{
		"/":             http.StatusOK,
		"/api/tree":     http.StatusOK,
		"/api/shares":   http.StatusNotFound,
		"/hello.md":     http.StatusOK,
		"/api/editors":  http.StatusOK,
		pwaManifestPath: http.StatusOK,
	} {
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, r.StatusCode, want)
		}
	}
}

// ── owner gate ──────────────────────────────────────────────

func TestOwnerGate_BlocksAnonymous(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()

	if r := e.get(c, "/notes/a.md?view=raw"); r.code != http.StatusSeeOther ||
		r.header.Get("Location") != "/login?next=%2Fnotes%2Fa.md%3Fview%3Draw" {
		t.Fatalf("page: %d %q", r.code, r.header.Get("Location"))
	}
	for _, p := range []string{"/api/tree", "/api/file?path=hello.md", "/api/raw?path=hello.md", "/api/shares", "/api/editors", "/ws", pwaManifestPath} {
		if r := e.get(c, p); r.code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", p, r.code)
		}
	}
	if r := e.do(c, "POST", "/api/open?path=hello.md&editor=vscode", nil, nil); r.code != http.StatusUnauthorized {
		t.Errorf("/api/open: %d", r.code)
	}
	if r := e.get(c, pwaIconPrefix+"icon-192.png"); r.code != http.StatusOK {
		t.Errorf("icons should stay public: %d", r.code)
	}
	if r := e.get(c, "/login"); r.code != http.StatusOK || !strings.Contains(r.body, `name="password"`) {
		t.Errorf("login page: %d", r.code)
	}
}

func TestOwnerLogin(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()

	r := e.postForm(c, "/login", url.Values{"password": {"wrong"}, "next": {"/x.md"}})
	if r.code != http.StatusUnauthorized || !strings.Contains(r.body, "Incorrect password") {
		t.Fatalf("wrong password: %d", r.code)
	}

	r = e.postForm(c, "/login", url.Values{"password": {ownerPW}, "next": {"/subdir/nested.md"}})
	if r.code != http.StatusSeeOther || r.header.Get("Location") != "/subdir/nested.md" {
		t.Fatalf("login: %d %q", r.code, r.header.Get("Location"))
	}
	ck := r.header.Get("Set-Cookie")
	for _, want := range []string{"rv_owner_" + instanceID(e.root) + "=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(ck, want) {
			t.Errorf("cookie %q missing %q", ck, want)
		}
	}
	if strings.Contains(ck, "Secure") {
		t.Error("plain-http login must not set a Secure cookie (it would be dropped)")
	}

	if r := e.get(c, "/api/tree"); r.code != http.StatusOK {
		t.Fatalf("api after login: %d", r.code)
	}
	if r := e.get(c, "/"); r.code != http.StatusOK || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("index after login: %d cache=%q", r.code, r.header.Get("Cache-Control"))
	}
	if r := e.get(c, "/login?next=/a.md"); r.code != http.StatusSeeOther || r.header.Get("Location") != "/a.md" {
		t.Fatalf("logged-in /login should bounce to next: %d", r.code)
	}
}

func TestOwnerLogin_NoOpenRedirect(t *testing.T) {
	e := newAuthEnv(t)
	for _, next := range []string{"//evil.example", "https://evil.example", "/\\evil.example"} {
		r := e.postForm(e.client(), "/login", url.Values{"password": {ownerPW}, "next": {next}})
		if r.header.Get("Location") != "/" {
			t.Errorf("next=%q redirected to %q", next, r.header.Get("Location"))
		}
	}
}

func TestOwnerLogin_SecureCookieBehindHTTPSTunnel(t *testing.T) {
	e := newAuthEnv(t)
	r := e.do(e.client(), "POST", "/login", strings.NewReader("password="+url.QueryEscape(ownerPW)), map[string]string{
		"Content-Type":      "application/x-www-form-urlencoded",
		"X-Forwarded-Proto": "https",
	})
	if !strings.Contains(r.header.Get("Set-Cookie"), "Secure") {
		t.Fatalf("cookie must be Secure behind https: %q", r.header.Get("Set-Cookie"))
	}
}

func TestOwnerLogin_CrossSiteRejected(t *testing.T) {
	e := newAuthEnv(t)
	r := e.do(e.client(), "POST", "/login", strings.NewReader("password="+url.QueryEscape(ownerPW)), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       "https://evil.example",
	})
	if r.code != http.StatusForbidden {
		t.Fatalf("cross-site login: %d", r.code)
	}
}

func TestOwnerLogin_RateLimited(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	for i := 0; i < loginMaxFailures; i++ {
		e.postForm(c, "/login", url.Values{"password": {"nope"}})
	}
	r := e.postForm(c, "/login", url.Values{"password": {ownerPW}})
	if r.code != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
		t.Fatalf("locked out: %d retry=%q", r.code, r.header.Get("Retry-After"))
	}
	// A different client IP (via the local tunnel header) isn't affected.
	r = e.do(c, "POST", "/login", strings.NewReader("password="+url.QueryEscape(ownerPW)), map[string]string{
		"Content-Type":     "application/x-www-form-urlencoded",
		"Cf-Connecting-IP": "203.0.113.50",
	})
	if r.code != http.StatusSeeOther {
		t.Fatalf("other IP: %d", r.code)
	}
	e.advance(loginLockout)
	if r := e.postForm(c, "/login", url.Values{"password": {ownerPW}}); r.code != http.StatusSeeOther {
		t.Fatalf("after lockout: %d", r.code)
	}
}

func TestOwnerLogin_ParallelGuessesCapped(t *testing.T) {
	e := newAuthEnv(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.postForm(e.client(), "/login", url.Values{"password": {"guess"}})
			if r.code == http.StatusUnauthorized {
				mu.Lock()
				checked++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if checked > loginMaxFailures {
		t.Fatalf("%d passwords checked in parallel, limit is %d", checked, loginMaxFailures)
	}
}

func TestRaw_SniffedHTMLIsSandboxed(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	page := "<!DOCTYPE html><html><script>alert(1)</script></html>"
	for _, name := range []string{"page", "page.unknownext", "page.shtml", "page.xht", "page.xsl"} {
		if err := os.WriteFile(filepath.Join(e.root, name), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
		r := e.get(c, "/api/raw?path="+name)
		ct := r.header.Get("Content-Type")
		if !strings.Contains(r.header.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s served as %q without sandbox", name, ct)
		}
	}
	// Ordinary files keep working without the sandbox (PDF viewers refuse it).
	os.WriteFile(filepath.Join(e.root, "doc.pdf"), []byte("%PDF-1.4\n"), 0o644)
	if r := e.get(c, "/api/raw?path=doc.pdf"); r.header.Get("Content-Security-Policy") != "" {
		t.Errorf("pdf got CSP %q", r.header.Get("Content-Security-Policy"))
	}
}

func TestOwnerSession_ExpiresAfter30Days(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	e.advance(sessionMaxAge - time.Minute)
	if r := e.get(c, "/api/tree"); r.code != http.StatusOK {
		t.Fatalf("day 29: %d", r.code)
	}
	e.advance(2 * time.Minute)
	// The jar may still hold the cookie; the server must reject the token.
	if r := e.get(c, "/api/tree"); r.code != http.StatusUnauthorized {
		t.Fatalf("after 30 days: %d", r.code)
	}
}

func TestOwnerSession_PasswordChangeLogsOut(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	changed := mustAuthServer(t, e.root, e.cfgDir, "new password", e.now)
	srv2 := httptest.NewServer(changed.handler())
	defer srv2.Close()
	if r := replayCookies(t, c, e.srv.URL, srv2.URL+"/api/tree"); r != http.StatusUnauthorized {
		t.Fatalf("old session after password change: %d", r)
	}
}

func TestOwnerSession_NotValidForAnotherInstance(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	// Same machine, same config dir (same secret), same password, other root.
	other := mustAuthServer(t, copyTestdata(t), e.cfgDir, ownerPW, e.now)
	srv2 := httptest.NewServer(other.handler())
	defer srv2.Close()
	if r := replayCookies(t, c, e.srv.URL, srv2.URL+"/api/tree"); r != http.StatusUnauthorized {
		t.Fatalf("cookie accepted by another instance: %d", r)
	}
	// ...even if the cookie is renamed to the other instance's name.
	u, _ := url.Parse(e.srv.URL)
	var val string
	for _, ck := range c.Jar.Cookies(u) {
		if strings.HasPrefix(ck.Name, "rv_owner_") {
			val = ck.Value
		}
	}
	req, _ := http.NewRequest("GET", srv2.URL+"/api/tree", nil)
	req.AddCookie(&http.Cookie{Name: ownerCookieName(other.inst), Value: val})
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("renamed cookie accepted: %d", r.StatusCode)
	}
}

// replayCookies sends the cookies c holds for fromURL to target.
func replayCookies(t *testing.T, c *http.Client, fromURL, target string) int {
	t.Helper()
	u, _ := url.Parse(fromURL)
	req, _ := http.NewRequest("GET", target, nil)
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r.StatusCode
}

func TestLogout(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	if r := e.do(c, "POST", "/logout", nil, map[string]string{"Origin": "https://evil.example"}); r.code != http.StatusForbidden {
		t.Fatalf("cross-site logout: %d", r.code)
	}
	r := e.postForm(c, "/logout", nil)
	if r.code != http.StatusSeeOther || r.header.Get("Location") != "/login" {
		t.Fatalf("logout: %d %q", r.code, r.header.Get("Location"))
	}
	if r := e.get(c, "/api/tree"); r.code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", r.code)
	}
}

func TestOwnerMutations_RequireSameOrigin(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	evil := map[string]string{"Origin": "https://evil.example", "Content-Type": "application/json"}
	if r := e.do(c, "POST", "/api/shares", strings.NewReader(`{"path":"hello.md"}`), evil); r.code != http.StatusForbidden {
		t.Errorf("cross-site create share: %d", r.code)
	}
	if r := e.do(c, "DELETE", "/api/shares?id=x", nil, evil); r.code != http.StatusForbidden {
		t.Errorf("cross-site delete share: %d", r.code)
	}
	if r := e.do(c, "POST", "/api/open?path=hello.md&editor=vscode", nil, evil); r.code != http.StatusForbidden {
		t.Errorf("cross-site open: %d", r.code)
	}
}

// ── shares API ──────────────────────────────────────────────

func TestSharesAPI_CreateListRevoke(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)

	sh := e.createShare(c, "subdir", "pw-for-bob", 3600)
	if !sh.IsDir || sh.Path != "subdir" || !sh.HasPassword || sh.Slug != "subdir" {
		t.Fatalf("share %+v", sh)
	}
	if !strings.HasPrefix(sh.URL, "/s/subdir-") || len(sh.URL) != len("/s/subdir-")+shareIDLen {
		t.Fatalf("url %q", sh.URL)
	}
	if sh.ExpiresAt == nil || !sh.ExpiresAt.Equal(e.now().Add(time.Hour)) {
		t.Fatalf("expiresAt %v", sh.ExpiresAt)
	}

	r := e.get(c, "/api/shares")
	if r.code != http.StatusOK || strings.Contains(r.body, "passwordHash") || strings.Contains(r.body, "$2a$") {
		t.Fatalf("list leaks hashes or failed: %d %s", r.code, r.body)
	}
	var list struct{ Shares []shareView }
	json.Unmarshal([]byte(r.body), &list)
	if len(list.Shares) != 1 || list.Shares[0].ID != sh.ID {
		t.Fatalf("list %+v", list)
	}

	if r := e.do(c, "DELETE", "/api/shares?id="+sh.ID, nil, map[string]string{"Origin": e.srv.URL}); r.code != http.StatusNoContent {
		t.Fatalf("revoke: %d", r.code)
	}
	if r := e.do(c, "DELETE", "/api/shares?id="+sh.ID, nil, map[string]string{"Origin": e.srv.URL}); r.code != http.StatusNotFound {
		t.Fatalf("revoke again: %d", r.code)
	}
}

func TestSharesAPI_ListMarksExpired(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	e.createShare(c, "hello.md", "", 60)
	e.advance(2 * time.Minute)
	var list struct{ Shares []shareView }
	json.Unmarshal([]byte(e.get(c, "/api/shares").body), &list)
	if len(list.Shares) != 1 || !list.Shares[0].Expired {
		t.Fatalf("list %+v", list)
	}
}

func TestSharesAPI_RejectsBadInput(t *testing.T) {
	e := newAuthEnv(t)
	c := e.client()
	e.login(c)
	for name, body := range map[string]string{
		"traversal":     `{"path":"../etc"}`,
		"missing":       `{"path":"nope.md"}`,
		"negative":      `{"path":"hello.md","expiresIn":-5}`,
		"too long":      `{"path":"hello.md","expiresIn":999999999999}`,
		"bad json":      `{"path":`,
		"long password": `{"path":"hello.md","password":"` + strings.Repeat("x", 100) + `"}`,
	} {
		r := e.do(c, "POST", "/api/shares", strings.NewReader(body), map[string]string{"Origin": e.srv.URL, "Content-Type": "application/json"})
		if r.code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
}

// ── visiting a share ────────────────────────────────────────

func TestShareVisit_PasswordFlowAndScope(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "pw-for-bob", 86400)

	bob := e.client()
	if r := e.get(bob, sh.URL); r.code != http.StatusMovedPermanently || r.header.Get("Location") != sh.URL+"/" {
		t.Fatalf("no trailing slash: %d %q", r.code, r.header.Get("Location"))
	}
	r := e.get(bob, sh.URL+"/")
	if r.code != http.StatusOK || !strings.Contains(r.body, `name="password"`) || strings.Contains(r.body, "nested.md") {
		t.Fatalf("prompt: %d", r.code)
	}
	if r.header.Get("X-Robots-Tag") == "" || r.header.Get("Referrer-Policy") != "same-origin" {
		t.Errorf("share pages must not be indexed or leak the link via Referer: %v", r.header)
	}
	if r := e.get(bob, sh.URL+"/api/tree"); r.code != http.StatusUnauthorized {
		t.Fatalf("locked api: %d", r.code)
	}
	if r := e.unlockShare(bob, sh, "wrong"); r.code != http.StatusUnauthorized {
		t.Fatalf("wrong share pw: %d", r.code)
	}
	r = e.unlockShare(bob, sh, "pw-for-bob")
	if r.code != http.StatusSeeOther || r.header.Get("Location") != sh.URL+"/" {
		t.Fatalf("unlock: %d %q", r.code, r.header.Get("Location"))
	}
	if ck := r.header.Get("Set-Cookie"); !strings.Contains(ck, "rv_share_"+sh.ID+"=") || !strings.Contains(ck, "Path=/s/") || !strings.Contains(ck, "HttpOnly") {
		t.Fatalf("share cookie %q", ck)
	}

	names := treeNamesFromBody(t, e.get(bob, sh.URL+"/api/tree?path=.").body)
	if len(names) != 2 || !names["nested.md"] || !names["README.md"] {
		t.Fatalf("share tree %v, want exactly subdir's nested.md and README.md", names)
	}
	if r := e.get(bob, sh.URL+"/api/file?path=nested.md"); r.code != http.StatusOK {
		t.Fatalf("file in share: %d", r.code)
	}
	for _, p := range []string{
		"/api/raw?path=../hello.md",
		"/api/raw?path=..%2Fhello.md",
		"/api/file?path=%2e%2e/hello.md",
	} {
		if r := e.get(bob, sh.URL+p); r.code < 400 || r.code >= 500 {
			t.Errorf("%s escaped the share: %d", p, r.code)
		}
	}
	if r := e.get(bob, sh.URL+"/api/files"); strings.Contains(r.body, "hello.md") {
		t.Errorf("files list leaks: %s", r.body)
	}
	for _, p := range []string{"/api/editors", "/api/shares", pwaManifestPath} {
		if r := e.get(bob, sh.URL+p); r.code != http.StatusNotFound {
			t.Errorf("share %s: %d, want 404", p, r.code)
		}
	}
	// The share session doesn't open the owner's area.
	for _, p := range []string{"/api/tree", "/api/shares", "/"} {
		if r := e.get(bob, p); r.code == http.StatusOK {
			t.Errorf("share visitor reached owner %s", p)
		}
	}
	// Deep links and the bare base serve the app.
	if r := e.get(bob, sh.URL+"/nested.md"); r.code != http.StatusOK || !strings.Contains(r.body, "<html") {
		t.Errorf("deep link: %d", r.code)
	}
}

func TestRawPathRoute_GatedAndScoped(t *testing.T) {
	e := newAuthEnv(t)
	anon := e.client()
	if r := e.get(anon, "/_raw/hello.md"); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous /_raw/: %d", r.code)
	}
	owner := e.client()
	e.login(owner)
	if r := e.get(owner, "/_raw/hello.md"); r.code != http.StatusOK {
		t.Fatalf("owner /_raw/: %d", r.code)
	}

	sh := e.createShare(owner, "subdir", "pw", 0)
	bob := e.client()
	if r := e.get(bob, sh.URL+"/_raw/nested.md"); r.code != http.StatusUnauthorized {
		t.Fatalf("locked share /_raw/: %d", r.code)
	}
	e.unlockShare(bob, sh, "pw")
	if r := e.get(bob, sh.URL+"/_raw/nested.md"); r.code != http.StatusOK || r.body == "" {
		t.Fatalf("share /_raw/: %d", r.code)
	}
	for _, p := range []string{"/_raw/%2e%2e/hello.md", "/_raw/..%2fhello.md", "/_raw/../hello.md"} {
		r := e.get(bob, sh.URL+p)
		if r.code == http.StatusOK && !strings.Contains(r.body, "<html") {
			t.Errorf("%s escaped the share: %d %q", p, r.code, r.body)
		}
	}
	if r := e.get(bob, sh.URL+"/api/nope"); r.code != http.StatusNotFound {
		t.Errorf("unknown share api path: %d", r.code)
	}
}

func TestShareVisit_CookieOnlyUnlocksItsShare(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	a := e.createShare(owner, "subdir", "pw-a", 0)
	b := e.createShare(owner, "hello.md", "pw-b", 0)

	bob := e.client()
	e.unlockShare(bob, a, "pw-a")
	if r := e.get(bob, b.URL+"/api/tree"); r.code != http.StatusUnauthorized {
		t.Fatalf("share A cookie opened share B: %d", r.code)
	}
	// Forge: present A's token under B's cookie name.
	u, _ := url.Parse(e.srv.URL + "/s/")
	var tok string
	for _, ck := range bob.Jar.Cookies(u) {
		if ck.Name == shareCookieName(a.ID) {
			tok = ck.Value
		}
	}
	req, _ := http.NewRequest("GET", e.srv.URL+b.URL+"/api/tree", nil)
	req.AddCookie(&http.Cookie{Name: shareCookieName(b.ID), Value: tok})
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged cookie: %d", r.StatusCode)
	}
}

func TestShareVisit_SlugIsCosmetic(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "", 0)
	for _, ref := range []string{"renamed-" + sh.ID, sh.ID} {
		if r := e.get(e.client(), "/s/"+ref+"/api/tree"); r.code != http.StatusOK {
			t.Errorf("%s: %d", ref, r.code)
		}
	}
}

func TestShareVisit_LinkOnlyShare(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "", 0)
	anon := e.client()
	r := e.get(anon, sh.URL+"/")
	if r.code != http.StatusOK || strings.Contains(r.body, `name="password"`) {
		t.Fatalf("link-only share should open directly: %d", r.code)
	}
	if r := e.get(anon, sh.URL+"/api/tree"); r.code != http.StatusOK {
		t.Fatalf("api: %d", r.code)
	}
}

func TestShareVisit_OwnerNeedsNoPassword(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "pw", 0)
	if r := e.get(owner, sh.URL+"/api/tree"); r.code != http.StatusOK {
		t.Fatalf("owner preview: %d", r.code)
	}
}

func TestShareVisit_SingleFile(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir/nested.md", "", 0)
	c := e.client()
	names := treeNamesFromBody(t, e.get(c, sh.URL+"/api/tree?path=.").body)
	if len(names) != 1 || !names["nested.md"] {
		t.Fatalf("file share tree %v", names)
	}
	if r := e.get(c, sh.URL+"/api/raw?path=nested.md"); r.code != http.StatusOK {
		t.Fatalf("the file: %d", r.code)
	}
	if err := os.WriteFile(filepath.Join(e.root, "subdir", "sibling.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := e.get(c, sh.URL+"/api/raw?path=sibling.md"); r.code != http.StatusBadRequest {
		t.Fatalf("sibling of shared file: %d", r.code)
	}
	if r := e.get(c, sh.URL+"/"); !strings.Contains(r.body, "<html") {
		t.Fatalf("index: %d", r.code)
	}
}

func TestShareVisit_RevokedExpiredMissing(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)

	revoked := e.createShare(owner, "subdir", "pw", 0)
	bob := e.client()
	e.unlockShare(bob, revoked, "pw")
	if r := e.get(bob, revoked.URL+"/api/tree"); r.code != http.StatusOK {
		t.Fatalf("before revoke: %d", r.code)
	}
	e.do(owner, "DELETE", "/api/shares?id="+revoked.ID, nil, map[string]string{"Origin": e.srv.URL})
	for _, p := range []string{"/", "/api/tree"} {
		if r := e.get(bob, revoked.URL+p); r.code != http.StatusNotFound {
			t.Errorf("revoked %s: %d", p, r.code)
		}
	}

	expiring := e.createShare(owner, "subdir", "", 3600)
	if r := e.get(bob, expiring.URL+"/api/tree"); r.code != http.StatusOK {
		t.Fatalf("before expiry: %d", r.code)
	}
	e.advance(time.Hour)
	if r := e.get(bob, expiring.URL+"/api/tree"); r.code != http.StatusNotFound {
		t.Fatalf("after expiry: %d", r.code)
	}

	gone := e.createShare(owner, "data.csv", "", 0)
	os.Remove(filepath.Join(e.root, "data.csv"))
	if r := e.get(bob, gone.URL+"/"); r.code != http.StatusNotFound {
		t.Fatalf("deleted target: %d", r.code)
	}

	for _, ref := range []string{"nope", "x-" + strings.Repeat("A", shareIDLen)} {
		if r := e.get(bob, "/s/"+ref+"/"); r.code != http.StatusNotFound {
			t.Errorf("unknown %s: %d", ref, r.code)
		}
	}
}

func TestShareVisit_SessionEndsWithShare(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "pw", 7200)
	bob := e.client()
	r := e.unlockShare(bob, sh, "pw")
	if !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=7200") {
		t.Fatalf("cookie lifetime: %q", r.header.Get("Set-Cookie"))
	}
}

func TestShareVisit_RateLimitedPerShare(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	a := e.createShare(owner, "subdir", "pw-a", 0)
	b := e.createShare(owner, "hello.md", "pw-b", 0)
	bob := e.client()
	for i := 0; i < loginMaxFailures; i++ {
		e.unlockShare(bob, a, "nope")
	}
	if r := e.unlockShare(bob, a, "pw-a"); r.code != http.StatusTooManyRequests {
		t.Fatalf("share A locked: %d", r.code)
	}
	if r := e.unlockShare(bob, b, "pw-b"); r.code != http.StatusSeeOther {
		t.Fatalf("share B unaffected: %d", r.code)
	}
	if r := e.postForm(bob, "/login", url.Values{"password": {ownerPW}}); r.code != http.StatusSeeOther {
		t.Fatalf("owner login unaffected: %d", r.code)
	}
}

func TestShareVisit_CrossSiteUnlockRejected(t *testing.T) {
	e := newAuthEnv(t)
	owner := e.client()
	e.login(owner)
	sh := e.createShare(owner, "subdir", "pw", 0)
	r := e.do(e.client(), "POST", sh.URL+"/login", strings.NewReader("password=pw"), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       "https://evil.example",
	})
	if r.code != http.StatusForbidden {
		t.Fatalf("cross-site unlock: %d", r.code)
	}
}

// ── page config ─────────────────────────────────────────────

func TestRenderIndex_InjectsConfig(t *testing.T) {
	raw := []byte(`<title>__ROOT_DIR_NAME__</title><script>var n = __ROOT_DIR_NAME_JS__; var RV = __RV_CONFIG_JS__;</script>`)
	out := string(renderIndex(raw, "notes</script>", pageConfig{Base: "/s/x-1", Mode: "share", File: "a.md"}))
	if !strings.Contains(out, `"base":"/s/x-1"`) || !strings.Contains(out, `"mode":"share"`) || !strings.Contains(out, `"file":"a.md"`) {
		t.Fatalf("config not injected: %s", out)
	}
	if strings.Contains(out, "</script>\"") || strings.Contains(out, "notes</script>") {
		t.Fatalf("names must be escaped inside <script>: %s", out)
	}
	local := string(prepareIndexHTML(raw, "repo"))
	if !strings.Contains(local, `"mode":"local"`) || !strings.Contains(local, `"file":null`) {
		t.Fatalf("local config: %s", local)
	}
}

func TestRawHTMLIsSandboxed(t *testing.T) {
	setRoot(t, "testdata")
	srv := newTestServer(t)
	defer srv.Close()
	r, err := http.Get(srv.URL + "/api/raw?path=sample.html")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("raw HTML must be served sandboxed, got CSP %q", csp)
	}
}

func TestServerConfigFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REPOVIEW_CONFIG_DIR", dir)

	t.Setenv("REPOVIEW_PASSWORD", "")
	t.Setenv("REPOVIEW_PASSWORD_HASH", "")
	cfg, err := serverConfigFromEnv("/srv/repo")
	if err != nil || cfg.owner != nil {
		t.Fatalf("no password: owner=%v err=%v", cfg.owner, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret")); err == nil {
		t.Fatal("local mode must not create state")
	}

	t.Setenv("REPOVIEW_PASSWORD", "pw")
	cfg, err = serverConfigFromEnv("/srv/repo")
	if err != nil || cfg.owner == nil || !cfg.owner.check("pw") || cfg.shares == nil || len(cfg.secret) != 32 {
		t.Fatalf("password mode: %+v %v", cfg, err)
	}
	if os.Getenv("REPOVIEW_PASSWORD") != "" {
		t.Fatal("password must be scrubbed from the environment")
	}

	t.Setenv("REPOVIEW_PASSWORD", "a")
	t.Setenv("REPOVIEW_PASSWORD_HASH", "b")
	if _, err := serverConfigFromEnv("/srv/repo"); err == nil {
		t.Fatal("both set must fail")
	}
}
