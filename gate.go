package main

// Imperative shell for access control: assembles the HTTP routes, checks
// sessions, serves the login and share-password pages and the shares API.
// Decisions are delegated to the pure functions in auth.go and scope.go.

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	maxShareExpiry   = 10 * 365 * 24 * time.Hour
	maxPasswordBytes = 72 // bcrypt's limit
	// shareCheckTTL: how long a share's validity is cached for websockets,
	// which otherwise re-read the store on every filesystem event.
	shareCheckTTL = 2 * time.Second
	sweepInterval = 30 * time.Second
)

type serverConfig struct {
	root   string
	owner  *ownerCredential // nil: no password configured (local mode, as before)
	secret []byte
	shares shareStore
	now    func() time.Time
	rand   io.Reader
}

type server struct {
	serverConfig
	inst    string
	limiter *limiter      // per client and login target
	targets *limiter      // per login target, all clients together
	checks  chan struct{} // semaphore for concurrent bcrypt checks

	validMu sync.Mutex
	valid   map[string]validity // share id -> cached shareStillValid result
	index   []byte              // raw embedded index.html
	hub     *hub
	api     *http.ServeMux // read-only API, shared by owner and share visitors
	watcher *fsnotify.Watcher
}

// serverConfigFromEnv turns on password login when REPOVIEW_PASSWORD or
// REPOVIEW_PASSWORD_HASH is set. The variables are removed from the
// environment afterwards so editors launched by /api/open don't inherit them.
func serverConfigFromEnv(root string) (serverConfig, error) {
	cfg := serverConfig{root: root}
	plain, hash := os.Getenv("REPOVIEW_PASSWORD"), os.Getenv("REPOVIEW_PASSWORD_HASH")
	os.Unsetenv("REPOVIEW_PASSWORD")
	os.Unsetenv("REPOVIEW_PASSWORD_HASH")
	if plain == "" && hash == "" {
		return cfg, nil
	}
	dir, err := configDir()
	if err != nil {
		return cfg, err
	}
	secret, err := loadOrCreateSecret(dir)
	if err != nil {
		return cfg, err
	}
	cred, err := newOwnerCredential(secret, plain, hash)
	if err != nil {
		return cfg, err
	}
	cfg.owner, cfg.secret = &cred, secret
	cfg.shares = newFileShareStore(dir, root)
	return cfg, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newServer(cfg serverConfig) *server {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.rand == nil {
		cfg.rand = cryptoRand.Reader
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	return &server{
		serverConfig: cfg,
		inst:         instanceID(cfg.root),
		limiter:      newLimiter(loginMaxFailures, loginLockout),
		targets:      newLimiter(loginTargetMax, loginLockout),
		checks:       make(chan struct{}, maxConcurrentChecks),
		valid:        map[string]validity{},
		index:        index,
		hub:          newHub(),
		api:          apiHandler(),
	}
}

func (s *server) authEnabled() bool { return s.owner != nil }

func (s *server) serveWS(w http.ResponseWriter, r *http.Request, alive func() bool) {
	handleWSScoped(w, r, s.hub, s.watcher, alive)
}

// watch starts delivering filesystem change events to websocket clients and
// periodically disconnects clients whose share was revoked or expired.
func (s *server) watch(w *fsnotify.Watcher) {
	s.watcher = w
	go watchLoop(w, s.hub)
	go func() {
		for range time.Tick(sweepInterval) {
			s.hub.sweep()
		}
	}()
}

// ── Routing ─────────────────────────────────────────────────

func (s *server) handler() http.Handler {
	rootName := filepath.Base(s.root)

	mode := "local"
	if s.authEnabled() {
		mode = "owner"
	}
	index := s.serveIndex(rootName, pageConfig{Mode: mode})

	app := http.NewServeMux() // what the owner (or anyone, in local mode) sees
	app.Handle("/api/", s.apiOr(index))
	app.Handle(rawPrefix, s.api)
	app.HandleFunc("/api/editors", handleEditors)
	app.Handle("/api/open", requireSameOrigin(http.HandlerFunc(handleOpen)))
	app.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { s.serveWS(w, r, nil) })
	registerPWARoutes(app, rootName)
	app.HandleFunc("/", index)
	if !s.authEnabled() {
		return localHeaders(s.withRootScope(app))
	}
	app.Handle("/api/shares", requireSameOrigin(http.HandlerFunc(s.handleShares)))

	public := http.NewServeMux()
	public.HandleFunc("/login", s.handleLogin)
	public.HandleFunc("/logout", s.handleLogout)
	public.HandleFunc("/s/", s.handleShareArea)
	public.Handle(pwaIconPrefix, app) // icons are harmless and fetched without cookies
	public.Handle("/", s.requireOwner(s.withRootScope(app)))
	return securityHeaders(public)
}

// withRootScope gives owner requests the whole served root as their scope.
func (s *server) withRootScope(next http.Handler) http.Handler {
	sc := scope{root: s.root}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(withScope(r.Context(), sc)))
	})
}

// securityHeaders applies to every response once the server is exposed.
// no-store keeps Cloudflare (and browsers) from caching private pages;
// handlers that serve public assets override it.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		// same-origin (not no-referrer): share URLs never leak to other sites,
		// while our own form POSTs keep a real Origin header (no-referrer makes
		// browsers send "Origin: null", which sameOrigin rightly refuses).
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// localHeaders: the minimum that still matters without a password, e.g. on
// a LAN (--host 0.0.0.0): no framing by other sites, no MIME sniffing.
func localHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiOr serves the read-only API. Unknown /api/... paths get the SPA when
// they name a real file or folder in the scope (a repo folder called "api",
// whose clean URLs start with /api/), else 404 so broken URLs fail visibly.
func (s *server) apiOr(index http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := s.api.Handler(r); pattern != "" {
			s.api.ServeHTTP(w, r)
			return
		}
		if p, err := scopeOf(r).resolve(strings.TrimPrefix(r.URL.Path, "/")); err == nil {
			if _, err := os.Stat(p); err == nil {
				index(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// isAPIPath: requests that get 401 rather than a login redirect.
func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, rawPrefix) || p == "/ws"
}

func (s *server) requireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.ownerSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		if isAPIPath(r.URL.Path) {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	})
}

// ── Sessions ────────────────────────────────────────────────

func (s *server) sessionClaims(r *http.Request, cookie string) (claims, bool) {
	ck, err := r.Cookie(cookie)
	if err != nil {
		return claims{}, false
	}
	c, err := verifyToken(s.secret, ck.Value, s.now())
	if err != nil || c.Inst != s.inst {
		return claims{}, false
	}
	return c, true
}

func (s *server) ownerSession(r *http.Request) bool {
	if !s.authEnabled() {
		return false
	}
	c, ok := s.sessionClaims(r, ownerCookieName(s.inst))
	return ok && c.Kind == kindOwner && constantTimeEqual(c.FP, s.owner.fp)
}

func (s *server) shareSession(r *http.Request, sh Share) bool {
	c, ok := s.sessionClaims(r, shareCookieName(sh.ID))
	return ok && c.Kind == kindShare && constantTimeEqual(c.Share, sh.ID) && constantTimeEqual(c.FP, sh.fingerprint())
}

func (s *server) setSession(w http.ResponseWriter, r *http.Request, name, cookiePath string, c claims) {
	maxAge := int(c.Exp - s.now().Unix())
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    signToken(s.secret, c),
		Path:     cookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name, cookiePath string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: cookiePath, MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
}

// checkPassword runs a rate-limited password check for one login target
// ("owner" or "share|<id>") and writes the failure response itself; it
// returns true only on success.
func (s *server) checkPassword(w http.ResponseWriter, r *http.Request, target string, check func(string) bool, page pageData) bool {
	now := s.now()
	wait, ok := s.limiter.attempt(target+"|"+rateKey(clientIP(r)), now)
	if ok {
		wait, ok = s.targets.attempt(target, now)
	}
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		page.Error = fmt.Sprintf("Too many attempts. Try again in %d minutes.", int(math.Ceil(wait.Minutes())))
		renderPage(w, http.StatusTooManyRequests, page)
		return false
	}
	select {
	case s.checks <- struct{}{}:
	case <-r.Context().Done():
		return false
	}
	good := check(r.PostFormValue("password"))
	<-s.checks
	if !good {
		page.Error = "Incorrect password."
		renderPage(w, http.StatusUnauthorized, page)
		return false
	}
	s.limiter.succeed(target + "|" + rateKey(clientIP(r)))
	s.targets.succeed(target)
	return true
}

// ── Owner login ─────────────────────────────────────────────

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	page := pageData{Title: "Sign in", Heading: "Sign in to RepoView", Action: "/login", Next: next, Button: "Sign in"}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if s.ownerSession(r) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		renderPage(w, http.StatusOK, page)
	case http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		if !s.checkPassword(w, r, "owner", s.owner.check, page) {
			return
		}
		s.setSession(w, r, ownerCookieName(s.inst), "/", claims{
			Kind: kindOwner, Inst: s.inst, FP: s.owner.fp, Exp: s.now().Add(sessionMaxAge).Unix(),
		})
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	clearCookie(w, r, ownerCookieName(s.inst), "/")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ── Share area: /s/<slug>-<id>/... ──────────────────────────

func (s *server) handleShareArea(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/s/")
	ref, sub, hasSlash := strings.Cut(rest, "/")
	sh, ok := s.lookupShare(w, ref)
	if !ok {
		return
	}
	base := "/s/" + ref
	if !hasSlash {
		target := base + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	name := filepath.Base(filepath.FromSlash(sh.Path))
	if sh.Path == "." {
		name = filepath.Base(s.root)
	}
	if sub == "login" && r.Method == http.MethodPost {
		s.shareLogin(w, r, sh, base, name)
		return
	}
	if !(sh.PasswordHash == "" || s.ownerSession(r) || s.shareSession(r, sh)) {
		if isAPIPath("/" + sub) {
			http.Error(w, "password required", http.StatusUnauthorized)
			return
		}
		renderPage(w, http.StatusOK, sharePromptPage(base, name))
		return
	}

	sc := sh.scopeIn(s.root)
	target := sc.root
	if sc.only != "" {
		target = filepath.Join(sc.root, sc.only)
	}
	if _, err := os.Stat(target); err != nil {
		renderPage(w, http.StatusNotFound, messagePage("Not found", "The shared item no longer exists."))
		return
	}

	cfg := pageConfig{Base: base, Mode: "share"}
	if sc.only != "" {
		cfg.File = sc.only
	}
	index := s.serveIndex(name, cfg)
	r2 := r.Clone(withScope(r.Context(), sc))
	switch {
	case sub == "ws":
		s.serveWS(w, r2, func() bool { return s.shareStillValid(sh.ID) })
	case isAPIPath("/" + sub):
		r2.URL.Path = "/" + sub
		r2.URL.RawPath = ""
		s.apiOr(index).ServeHTTP(w, r2)
	default:
		index(w, r2)
	}
}

// lookupShare resolves a ref to an active share, writing a 404 otherwise.
func (s *server) lookupShare(w http.ResponseWriter, ref string) (Share, bool) {
	notFound := func() {
		renderPage(w, http.StatusNotFound, messagePage("Link not available", "This link is invalid, has expired or was revoked."))
	}
	id, ok := parseShareRef(ref)
	if !ok {
		notFound()
		return Share{}, false
	}
	sh, found, err := s.shares.get(id)
	if err != nil {
		log.Printf("shares: %v", err)
		http.Error(w, "share store unavailable", http.StatusInternalServerError)
		return Share{}, false
	}
	if !found || !sh.active(s.now()) {
		notFound()
		return Share{}, false
	}
	return sh, true
}

type validity struct {
	ok bool
	at time.Time
}

// shareStillValid reports whether a share exists and is active, caching the
// answer briefly (revocation takes effect within shareCheckTTL).
func (s *server) shareStillValid(id string) bool {
	now := s.now()
	s.validMu.Lock()
	if v, ok := s.valid[id]; ok && now.Sub(v.at) < shareCheckTTL {
		s.validMu.Unlock()
		return v.ok
	}
	s.validMu.Unlock()
	sh, found, err := s.shares.get(id)
	ok := err == nil && found && sh.active(now)
	s.validMu.Lock()
	s.valid[id] = validity{ok: ok, at: now}
	s.validMu.Unlock()
	return ok
}

func (s *server) shareLogin(w http.ResponseWriter, r *http.Request, sh Share, base, name string) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if !s.checkPassword(w, r, "share|"+sh.ID, sh.checkPassword, sharePromptPage(base, name)) {
		return
	}
	s.setSession(w, r, shareCookieName(sh.ID), "/s/", claims{
		Kind: kindShare, Inst: s.inst, Share: sh.ID, FP: sh.fingerprint(), Exp: sh.sessionExpiry(s.now()).Unix(),
	})
	http.Redirect(w, r, base+"/", http.StatusSeeOther)
}

// ── Shares API (owner only) ─────────────────────────────────

type shareView struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	URL         string     `json:"url"`
	Path        string     `json:"path"`
	IsDir       bool       `json:"isDir"`
	HasPassword bool       `json:"hasPassword"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	Expired     bool       `json:"expired"`
}

func viewOf(sh Share, now time.Time) shareView {
	return shareView{
		ID: sh.ID, Slug: sh.Slug, URL: sh.URL(), Path: sh.Path, IsDir: sh.IsDir,
		HasPassword: sh.PasswordHash != "", CreatedAt: sh.CreatedAt, ExpiresAt: sh.ExpiresAt,
		Expired: !sh.active(now),
	}
}

type createShareRequest struct {
	Path      string `json:"path"`
	Password  string `json:"password"`
	ExpiresIn int64  `json:"expiresIn"` // seconds; 0 = never
}

// validateShareRequest checks a create request against the owner's scope and
// returns the normalised path and whether it is a directory.
func validateShareRequest(req createShareRequest, root string) (string, bool, time.Duration, error) {
	rel, ok := checkRel(req.Path, "", false)
	if !ok {
		return "", false, 0, errors.New("invalid path")
	}
	if req.ExpiresIn < 0 || req.ExpiresIn > int64(maxShareExpiry/time.Second) {
		return "", false, 0, errors.New("expiresIn must be between 0 (never) and 10 years")
	}
	if len(req.Password) > maxPasswordBytes {
		return "", false, 0, fmt.Errorf("password must be at most %d bytes", maxPasswordBytes)
	}
	abs, err := scope{root: root}.resolve(rel)
	if err != nil {
		return "", false, 0, errors.New("invalid path")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false, 0, errors.New("no such file or folder")
	}
	return rel, info.IsDir(), time.Duration(req.ExpiresIn) * time.Second, nil
}

func (s *server) handleShares(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	switch r.Method {
	case http.MethodGet:
		list, err := s.shares.list()
		if err != nil {
			log.Printf("shares: %v", err)
			http.Error(w, "share store unavailable", http.StatusInternalServerError)
			return
		}
		views := make([]shareView, 0, len(list))
		for _, sh := range list {
			views = append(views, viewOf(sh, now))
		}
		writeJSON(w, http.StatusOK, map[string]any{"shares": views})

	case http.MethodPost:
		var req createShareRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		rel, isDir, expiresIn, err := validateShareRequest(req, s.root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sh, err := newShare(s.rand, now, rel, isDir, req.Password, expiresIn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if rel == "." {
			sh.Slug = slugify(filepath.Base(s.root))
		}
		if err := s.shares.add(sh); err != nil {
			log.Printf("shares: %v", err)
			http.Error(w, "could not save share", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, viewOf(sh, now))

	case http.MethodDelete:
		removed, err := s.shares.remove(r.URL.Query().Get("id"))
		if err != nil {
			log.Printf("shares: %v", err)
			http.Error(w, "could not revoke share", http.StatusInternalServerError)
			return
		}
		if !removed {
			http.Error(w, "no such share", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// ── The SPA ─────────────────────────────────────────────────

// pageConfig is injected into index.html as `var RV = {...}`.
type pageConfig struct {
	Base string // URL prefix for API, websocket and page URLs ("" or "/s/<ref>")
	Mode string // local | owner | share
	File string // single-file share: the only file
}

func (s *server) serveIndex(rootName string, cfg pageConfig) http.HandlerFunc {
	html := renderIndex(s.index, rootName, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(html)
	}
}
