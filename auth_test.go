package main

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var testSecret = bytes.Repeat([]byte{7}, 32)

// ── session tokens ──────────────────────────────────────────

func TestToken_RoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := claims{Kind: kindShare, Inst: "abc", Share: "id1", FP: "fp", Exp: now.Add(time.Hour).Unix()}
	got, err := verifyToken(testSecret, signToken(testSecret, c), now)
	if err != nil || got != c {
		t.Fatalf("verify = %+v, %v; want %+v", got, err, c)
	}
}

func TestToken_Rejects(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	good := signToken(testSecret, claims{Kind: kindOwner, Inst: "i", FP: "f", Exp: now.Add(time.Hour).Unix()})
	expired := signToken(testSecret, claims{Kind: kindOwner, Inst: "i", FP: "f", Exp: now.Add(-time.Second).Unix()})
	payload, mac, _ := strings.Cut(good, ".")
	other := bytes.Repeat([]byte{8}, 32)

	for name, tok := range map[string]string{
		"empty":           "",
		"no dot":          payload,
		"expired":         expired,
		"tampered body":   "x" + payload + "." + mac,
		"tampered mac":    payload + "." + mac[:len(mac)-2] + "AA",
		"swapped payload": strings.Split(expired, ".")[0] + "." + mac,
		"garbage":         "!!!.???",
	} {
		if _, err := verifyToken(testSecret, tok, now); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if _, err := verifyToken(other, good, now); err == nil {
		t.Error("token signed with another secret must be rejected")
	}
}

// ── owner credential ────────────────────────────────────────

func TestOwnerCredential_Plain(t *testing.T) {
	a, err := newOwnerCredential(testSecret, "hunter2", "")
	if err != nil {
		t.Fatal(err)
	}
	if !a.check("hunter2") || a.check("hunter3") || a.check("") {
		t.Fatal("check() mismatch")
	}
	b, _ := newOwnerCredential(testSecret, "hunter2", "")
	if a.fp != b.fp {
		t.Fatal("fingerprint must be stable across restarts for the same password")
	}
	c, _ := newOwnerCredential(testSecret, "changed", "")
	if a.fp == c.fp {
		t.Fatal("changing the password must change the fingerprint (logs sessions out)")
	}
}

func TestOwnerCredential_Hash(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	a, err := newOwnerCredential(testSecret, "", string(h))
	if err != nil {
		t.Fatal(err)
	}
	if !a.check("s3cret") || a.check("nope") {
		t.Fatal("check() mismatch")
	}
	if _, err := newOwnerCredential(testSecret, "", "not-a-bcrypt-hash"); err == nil {
		t.Fatal("invalid hash must be rejected at startup")
	}
	if _, err := newOwnerCredential(testSecret, "x", string(h)); err == nil {
		t.Fatal("setting both password and hash is ambiguous and must be rejected")
	}
}

// ── rate limiter ────────────────────────────────────────────

func TestLimiter(t *testing.T) {
	l := newLimiter(5, 15*time.Minute)
	now := time.Unix(1_700_000_000, 0)

	for i := 0; i < 5; i++ {
		if _, ok := l.attempt("k", now); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	wait, ok := l.attempt("k", now.Add(time.Minute))
	if ok || wait != 14*time.Minute {
		t.Fatalf("6th attempt: ok=%v wait=%v", ok, wait)
	}
	if _, ok := l.attempt("other", now); !ok {
		t.Fatal("keys must be independent")
	}
	if _, ok := l.attempt("k", now.Add(15*time.Minute)); !ok {
		t.Fatal("lockout must expire")
	}

	// Attempts spread out beyond the window don't accumulate.
	l2 := newLimiter(5, 15*time.Minute)
	for i := 0; i < 10; i++ {
		if _, ok := l2.attempt("k", now.Add(time.Duration(i)*16*time.Minute)); !ok {
			t.Fatalf("spread attempt %d refused", i)
		}
	}

	// Success clears the counter.
	l3 := newLimiter(5, 15*time.Minute)
	for i := 0; i < 4; i++ {
		l3.attempt("k", now)
	}
	l3.succeed("k")
	for i := 0; i < 5; i++ {
		if _, ok := l3.attempt("k", now); !ok {
			t.Fatal("success must reset the counter")
		}
	}
}

// The attempt is reserved before the (slow) password check, so a burst of
// parallel guesses can't all slip past the limit.
func TestLimiter_ConcurrentAttemptsCapped(t *testing.T) {
	l := newLimiter(5, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := l.attempt("k", now); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 5 {
		t.Fatalf("%d attempts allowed, want 5", allowed)
	}
}

// Flooding the limiter with fresh keys must not release locked-out keys.
func TestLimiter_FloodKeepsLockouts(t *testing.T) {
	l := newLimiter(5, time.Hour)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		l.attempt("victim", now)
	}
	for i := 0; i < limiterMaxEntries*2; i++ {
		l.attempt(fmt.Sprintf("flood-%d", i), now)
	}
	if _, ok := l.attempt("victim", now); ok {
		t.Fatal("flood released the lockout")
	}
	if n := len(l.entries); n > limiterMaxEntries {
		t.Fatalf("limiter grew to %d entries", n)
	}
}

// ── request facts ───────────────────────────────────────────

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("Cf-Connecting-IP", "203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("behind local tunnel: %q", got)
	}
	r.RemoteAddr = "[::1]:5555"
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("behind local tunnel (v6): %q", got)
	}
	r.RemoteAddr = "198.51.100.4:5555"
	if got := clientIP(r); got != "198.51.100.4" {
		t.Errorf("direct client must not be able to spoof the header: %q", got)
	}
}

func TestIsHTTPS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	if isHTTPS(r) {
		t.Error("plain local request")
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if !isHTTPS(r) {
		t.Error("tunnel says https")
	}
	r.RemoteAddr = "198.51.100.4:1"
	if isHTTPS(r) {
		t.Error("untrusted proxy header")
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(host string, h map[string]string) bool {
		r := httptest.NewRequest("POST", "/api/shares", nil)
		r.Host = host
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return sameOrigin(r)
	}
	if !mk("docs.example.com", map[string]string{"Origin": "https://docs.example.com"}) {
		t.Error("same origin")
	}
	if mk("docs.example.com", map[string]string{"Origin": "https://evil.example"}) {
		t.Error("cross origin")
	}
	if mk("docs.example.com", map[string]string{"Origin": "null"}) {
		t.Error("opaque origin")
	}
	if mk("docs.example.com", map[string]string{"Sec-Fetch-Site": "cross-site"}) {
		t.Error("cross-site fetch metadata")
	}
	if !mk("docs.example.com", nil) {
		t.Error("non-browser clients send neither header")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "/",
		"/notes/a.md":      "/notes/a.md",
		"/a?b=c":           "/a?b=c",
		"//evil.com":       "/",
		"/\\evil.com":      "/",
		"https://evil.com": "/",
		"javascript:x":     "/",
		"/s/abc/":          "/s/abc/",
		"/\t/evil.com":     "/",
		"/\r\n/evil.com":   "/",
		"/%09/evil.com":    "/%09/evil.com", // still encoded: a harmless local path
		"/a\x7f":           "/",
		"/caf\u00e9.md":    "/caf\u00e9.md",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstanceID(t *testing.T) {
	a, b := instanceID("/home/u/repo"), instanceID("/home/u/other")
	if a == b || len(a) != 16 || instanceID("/home/u/repo") != a {
		t.Fatalf("instanceID: %q %q", a, b)
	}
}

// ── shares (pure parts) ─────────────────────────────────────

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Quarterly Report.md":   "quarterly-report",
		"subdir":                "subdir",
		"notes/2026 Plan.v2.md": "2026-plan-v2",
		"Tử vi.md":              "t-vi",
		"!!!":                   "share",
		".":                     "share",
		strings.Repeat("a", 80): strings.Repeat("a", 40),
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewShareID(t *testing.T) {
	id, err := newShareID(bytes.NewReader(bytes.Repeat([]byte{0, 61, 62, 255, 123}, 50)))
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != shareIDLen || !validShareID(id) {
		t.Fatalf("id %q", id)
	}
	if _, err := newShareID(bytes.NewReader(nil)); err == nil {
		t.Fatal("exhausted randomness must be an error, not a weak id")
	}
}

func TestParseShareRef(t *testing.T) {
	id := strings.Repeat("A", shareIDLen)
	for in, want := range map[string]string{
		"quarterly-report-" + id: id,
		id:                       id,
		"a-b-c-" + id:            id,
	} {
		if got, ok := parseShareRef(in); !ok || got != want {
			t.Errorf("parseShareRef(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "short", "slug-" + strings.Repeat("A", shareIDLen-1) + "!", "slug-" + strings.Repeat("A", shareIDLen-1)} {
		if _, ok := parseShareRef(bad); ok {
			t.Errorf("parseShareRef(%q) should fail", bad)
		}
	}
}

func TestShareActiveAndSessionExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	never := Share{}
	if !never.active(now) {
		t.Fatal("no expiry = active")
	}
	if got := never.sessionExpiry(now); !got.Equal(now.Add(sessionMaxAge)) {
		t.Fatalf("never-expiring share session = %v", got)
	}
	soon := now.Add(time.Hour)
	s := Share{ExpiresAt: &soon}
	if !s.active(now) || s.active(soon) {
		t.Fatal("active() boundary")
	}
	if got := s.sessionExpiry(now); !got.Equal(soon) {
		t.Fatalf("session must not outlive the share: %v", got)
	}
}

func TestShareScope(t *testing.T) {
	dir := Share{Path: "docs/sub", IsDir: true}
	if sc := dir.scopeIn("/r"); sc.root != "/r/docs/sub" || sc.only != "" || !sc.restricted {
		t.Fatalf("dir scope %+v", sc)
	}
	file := Share{Path: "docs/a.md"}
	if sc := file.scopeIn("/r"); sc.root != "/r/docs" || sc.only != "a.md" || !sc.restricted {
		t.Fatalf("file scope %+v", sc)
	}
	rootShare := Share{Path: ".", IsDir: true}
	if sc := rootShare.scopeIn("/r"); sc.root != "/r" {
		t.Fatalf("root scope %+v", sc)
	}
}

func TestNewShare(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	rnd := bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 100))
	s, err := newShare(rnd, now, "notes/Q3 Report.md", false, "secret", 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if s.Slug != "q3-report" || s.Path != "notes/Q3 Report.md" || s.IsDir || !s.CreatedAt.Equal(now) {
		t.Fatalf("share %+v", s)
	}
	if s.ExpiresAt == nil || !s.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("expiry %v", s.ExpiresAt)
	}
	if s.PasswordHash == "" || s.PasswordHash == "secret" || bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte("secret")) != nil {
		t.Fatal("password must be stored as bcrypt")
	}
	if !s.checkPassword("secret") || s.checkPassword("nope") {
		t.Fatal("checkPassword mismatch")
	}
	if s.URL() != "/s/q3-report-"+s.ID {
		t.Fatalf("url %q", s.URL())
	}

	open, _ := newShare(bytes.NewReader(bytes.Repeat([]byte{9}, 100)), now, "docs", true, "", 0)
	if open.PasswordHash != "" || open.ExpiresAt != nil || !open.checkPassword("") {
		t.Fatalf("link-only, never-expiring share: %+v", open)
	}
	if _, err := newShare(rnd, now, "x", false, "", -time.Second); err == nil {
		t.Fatal("negative expiry must be rejected")
	}
}

func TestShareFingerprintChangesWithPassword(t *testing.T) {
	a := Share{ID: "x", PasswordHash: "h1"}
	b := Share{ID: "x", PasswordHash: "h2"}
	if a.fingerprint() == b.fingerprint() {
		t.Fatal("fingerprint must depend on the password hash")
	}
}
