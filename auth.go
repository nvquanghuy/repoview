package main

// Functional core for authentication and sharing: no I/O here except what
// callers inject (randomness, clock). The HTTP shell lives in gate.go and
// persistence in sharestore.go.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionMaxAge     = 30 * 24 * time.Hour
	loginMaxFailures  = 5
	loginLockout      = 15 * time.Minute
	limiterMaxEntries = 10_000
	// loginTargetMax caps attempts against one login (the owner's, or one
	// share's) from all clients together, so rotating IPs doesn't help.
	loginTargetMax = 50
	// maxConcurrentChecks bounds CPU spent on bcrypt by unauthenticated users.
	maxConcurrentChecks = 4
)

// ── Session tokens ──────────────────────────────────────────

const (
	kindOwner = "owner"
	kindShare = "share"
)

// claims is what a session cookie asserts. Inst binds it to one served root
// so several repoview instances on one host (sharing a secret) can't accept
// each other's cookies. FP is a fingerprint of the credential that was
// checked at login; when the credential changes, old sessions die.
type claims struct {
	Kind  string `json:"k"`
	Inst  string `json:"i"`
	Share string `json:"s,omitempty"`
	FP    string `json:"f"`
	Exp   int64  `json:"e"`
}

var errBadToken = errors.New("invalid session")

func mac(secret []byte, msg string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// signToken returns base64url(json(claims)) + "." + base64url(HMAC-SHA256).
func signToken(secret []byte, c claims) string {
	body, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac(secret, payload))
}

func verifyToken(secret []byte, tok string, now time.Time) (claims, error) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return claims{}, errBadToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac(secret, payload)) {
		return claims{}, errBadToken
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return claims{}, errBadToken
	}
	var c claims
	if err := json.Unmarshal(body, &c); err != nil {
		return claims{}, errBadToken
	}
	if now.Unix() >= c.Exp {
		return claims{}, errBadToken
	}
	return c, nil
}

// ── Owner credential ────────────────────────────────────────

type ownerCredential struct {
	hash []byte // bcrypt
	fp   string // stable fingerprint of the configured password
}

// newOwnerCredential builds the owner credential from exactly one of a
// plaintext password or a bcrypt hash (REPOVIEW_PASSWORD / _HASH).
func newOwnerCredential(secret []byte, plain, bcryptHash string) (ownerCredential, error) {
	switch {
	case plain != "" && bcryptHash != "":
		return ownerCredential{}, errors.New("set only one of REPOVIEW_PASSWORD and REPOVIEW_PASSWORD_HASH")
	case plain != "":
		h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
		if err != nil {
			return ownerCredential{}, err
		}
		// bcrypt salts differ per run, so fingerprint the password itself
		// (keyed, so the fingerprint reveals nothing) to keep sessions valid
		// across restarts.
		return ownerCredential{hash: h, fp: hex.EncodeToString(mac(secret, "owner-password:"+plain))[:16]}, nil
	case bcryptHash != "":
		if _, err := bcrypt.Cost([]byte(bcryptHash)); err != nil {
			return ownerCredential{}, fmt.Errorf("REPOVIEW_PASSWORD_HASH is not a bcrypt hash: %w", err)
		}
		sum := sha256.Sum256([]byte(bcryptHash))
		return ownerCredential{hash: []byte(bcryptHash), fp: hex.EncodeToString(sum[:])[:16]}, nil
	}
	return ownerCredential{}, errors.New("no owner password configured")
}

func (o ownerCredential) check(pw string) bool {
	return bcrypt.CompareHashAndPassword(o.hash, []byte(pw)) == nil
}

// ── Rate limiter ────────────────────────────────────────────

// limiter allows max password attempts per key within a window that starts
// at the key's first attempt; a success resets the key. Attempts are
// reserved *before* the slow password check, so parallel guesses can't race
// past the limit. The clock is passed in so behaviour is deterministic.
type limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	entries map[string]*attempts
}

type attempts struct {
	count int
	start time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, entries: map[string]*attempts{}}
}

// attempt reserves one try for key. When refused it returns how long until
// the key may try again.
func (l *limiter) attempt(key string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.entries[key]
	if ok && !now.Before(a.start.Add(l.window)) {
		delete(l.entries, key)
		ok = false
	}
	if !ok {
		if len(l.entries) >= limiterMaxEntries && !l.prune(now) {
			// Full of live entries: refuse rather than forget lockouts.
			return l.window, false
		}
		a = &attempts{start: now}
		l.entries[key] = a
	}
	if a.count >= l.max {
		return a.start.Add(l.window).Sub(now), false
	}
	a.count++
	return 0, true
}

func (l *limiter) succeed(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// prune drops expired entries, then entries that aren't locked out. Locked
// keys are never dropped, so a flood of fresh keys can't release them. It
// reports whether there is now room.
func (l *limiter) prune(now time.Time) bool {
	for k, a := range l.entries {
		if !now.Before(a.start.Add(l.window)) {
			delete(l.entries, k)
		}
	}
	for k, a := range l.entries {
		if len(l.entries) < limiterMaxEntries*9/10 {
			break
		}
		if a.count < l.max {
			delete(l.entries, k)
		}
	}
	return len(l.entries) < limiterMaxEntries
}

// ── Request facts ───────────────────────────────────────────

// fromLocalProxy reports whether the request came from this machine (e.g.
// cloudflared). Only then are proxy headers trusted; a client reaching the
// port directly could forge them.
func fromLocalProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// clientIP is the visitor's address: Cloudflare's Cf-Connecting-IP when the
// request comes through a local tunnel, else the TCP peer. X-Forwarded-For is
// deliberately ignored: other local proxies pass client-supplied values on.
func clientIP(r *http.Request) string {
	if fromLocalProxy(r) {
		if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-IP")); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateKey groups addresses one client is likely to control: an IPv6 /64
// counts as a single client, since anyone holding one can rotate through it.
func rateKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return (&net.IPNet{IP: parsed.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return fromLocalProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sameOrigin guards state-changing requests against CSRF. Browsers send
// Origin (and Sec-Fetch-Site) on cross-site POSTs; non-browser clients send
// neither and carry no ambient cookies to abuse.
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// safeNext keeps post-login redirects on this site: a single leading slash,
// no backslashes and no control characters (browsers strip tabs and newlines,
// which would turn "/\t/evil.com" into "//evil.com").
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	for _, r := range next {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return next
}

// instanceID identifies the served root; it namespaces cookies and share
// files so several repoview instances on one machine don't collide.
func instanceID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:16]
}

func ownerCookieName(inst string) string { return "rv_owner_" + inst }
func shareCookieName(id string) string   { return "rv_share_" + id }

// ── Shares ──────────────────────────────────────────────────

const (
	shareIDLen      = 22 // ~131 bits from a 62-letter alphabet
	shareIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	slugMaxLen      = 40
)

// Share grants access to one file or folder (Path, relative to the root).
type Share struct {
	ID           string     `json:"id"`
	Slug         string     `json:"slug"`
	Path         string     `json:"path"`
	IsDir        bool       `json:"isDir"`
	PasswordHash string     `json:"passwordHash,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
}

// newShareID draws an unbiased base62 id from rnd (rejection sampling).
func newShareID(rnd io.Reader) (string, error) {
	out := make([]byte, 0, shareIDLen)
	buf := make([]byte, 32)
	for len(out) < shareIDLen {
		if _, err := io.ReadFull(rnd, buf); err != nil {
			return "", fmt.Errorf("share id: %w", err)
		}
		for _, b := range buf {
			if b < 248 { // 248 = 4*62; larger values would bias the draw
				out = append(out, shareIDAlphabet[b%62])
				if len(out) == shareIDLen {
					break
				}
			}
		}
	}
	return string(out), nil
}

func validShareID(id string) bool {
	if len(id) != shareIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(shareIDAlphabet, rune(id[i])) {
			return false
		}
	}
	return true
}

// parseShareRef extracts the id from "<slug>-<id>" (or a bare id). The slug
// is cosmetic and never trusted.
func parseShareRef(ref string) (string, bool) {
	if len(ref) < shareIDLen {
		return "", false
	}
	id := ref[len(ref)-shareIDLen:]
	if !validShareID(id) {
		return "", false
	}
	if len(ref) > shareIDLen && ref[len(ref)-shareIDLen-1] != '-' {
		return "", false
	}
	return id, true
}

// slugify makes a short, URL-safe hint from a file or folder name.
func slugify(p string) string {
	name := path.Base(strings.ReplaceAll(p, `\`, "/"))
	if name != "." && name != "/" {
		name = strings.TrimSuffix(name, path.Ext(name))
	} else {
		name = ""
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := b.String()
	if len(s) > slugMaxLen {
		s = s[:slugMaxLen]
	}
	s = strings.Trim(s, "-")
	if s == "" {
		return "share"
	}
	return s
}

// newShare creates a share. An empty password makes a link-only share;
// expiresIn == 0 means it never expires.
func newShare(rnd io.Reader, now time.Time, relPath string, isDir bool, password string, expiresIn time.Duration) (Share, error) {
	if expiresIn < 0 {
		return Share{}, errors.New("expiry must not be negative")
	}
	id, err := newShareID(rnd)
	if err != nil {
		return Share{}, err
	}
	s := Share{ID: id, Slug: slugify(relPath), Path: relPath, IsDir: isDir, CreatedAt: now}
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return Share{}, err
		}
		s.PasswordHash = string(h)
	}
	if expiresIn > 0 {
		exp := now.Add(expiresIn)
		s.ExpiresAt = &exp
	}
	return s, nil
}

func (s Share) Ref() string { return s.Slug + "-" + s.ID }
func (s Share) URL() string { return "/s/" + s.Ref() }

func (s Share) active(now time.Time) bool {
	return s.ExpiresAt == nil || now.Before(*s.ExpiresAt)
}

// sessionExpiry: a visitor's session ends with the share, capped at 30 days.
func (s Share) sessionExpiry(now time.Time) time.Time {
	exp := now.Add(sessionMaxAge)
	if s.ExpiresAt != nil && s.ExpiresAt.Before(exp) {
		return *s.ExpiresAt
	}
	return exp
}

func (s Share) checkPassword(pw string) bool {
	if s.PasswordHash == "" {
		return true
	}
	return bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte(pw)) == nil
}

func (s Share) fingerprint() string {
	sum := sha256.Sum256([]byte(s.ID + "\x00" + s.PasswordHash))
	return hex.EncodeToString(sum[:])[:16]
}

// scopeIn is the slice of root a share's visitors may see.
func (s Share) scopeIn(root string) scope {
	if s.IsDir {
		return scope{root: joinRel(root, s.Path), restricted: true}
	}
	return scope{root: joinRel(root, path.Dir(s.Path)), only: path.Base(s.Path), restricted: true}
}

func joinRel(root, rel string) string {
	if rel == "." || rel == "" {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
