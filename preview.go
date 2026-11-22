package main

// HTML previews run in a sandboxed iframe (opaque origin), so the browser
// treats their requests as cross-site and drops our SameSite=Lax cookies:
// relative images and stylesheets inside a previewed page would get 401.
// Instead the page is loaded from a capability URL,
//
//	/_preview/<token>/<path>            (owner)
//	/s/<ref>/_preview/<token>/<path>    (share)
//
// so relative URLs resolve under the same token and need no cookies. The
// token is a signed claim (kind "preview") bound to the instance and to the
// owner's or the share's credential fingerprint, valid for previewMaxAge;
// revoking the share or changing the owner password kills it. Responses
// carry `connect-src 'none'`, so a page's scripts can't use the token to
// read other files.

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	previewPrefix = "/_preview/"
	previewMaxAge = 12 * time.Hour
)

type previewKey struct{}

// withPreview attaches a function that turns a scope-relative file path into
// its preview URL.
func withPreview(ctx context.Context, mint func(rel string) string) context.Context {
	return context.WithValue(ctx, previewKey{}, mint)
}

// previewURLFor is the URL the SPA should load an HTML file's preview from.
// Without a password there are no cookies to lose: plain /_raw/.
func previewURLFor(r *http.Request, rel string) string {
	if mint, ok := r.Context().Value(previewKey{}).(func(string) string); ok {
		return mint(rel)
	}
	return rawPrefix + escapePath(rel)
}

func escapePath(rel string) string {
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// previewMinter returns a mint function for one credential (share id "" for
// the owner) under URL prefix base ("" or "/s/<ref>").
func (s *server) previewMinter(base, shareID, fp string) func(string) string {
	return func(rel string) string {
		tok := signToken(s.secret, claims{
			Kind: kindPreview, Inst: s.inst, Share: shareID, FP: fp,
			Exp: s.now().Add(previewMaxAge).Unix(),
		})
		return base + previewPrefix + tok + "/" + escapePath(rel)
	}
}

// splitPreview splits "<token>/<path>" (the part after .../_preview/).
func splitPreview(rest string) (tok, rel string, ok bool) {
	tok, rel, ok = strings.Cut(rest, "/")
	return tok, rel, ok && tok != "" && rel != ""
}

// checkPreviewToken verifies a preview token for one credential.
func (s *server) checkPreviewToken(tok, shareID, fp string) bool {
	c, err := verifyToken(s.secret, tok, s.now())
	return err == nil && c.Kind == kindPreview && c.Inst == s.inst &&
		constantTimeEqual(c.Share, shareID) && constantTimeEqual(c.FP, fp)
}

// servePreview serves rel from sc after a token check. Paths must be clean:
// ".." is refused by serveRawFile, and anything the router would rewrite is
// refused here.
func servePreview(w http.ResponseWriter, r *http.Request, sc scope, rel string) {
	if path.Clean("/"+rel) != "/"+rel {
		http.NotFound(w, r)
		return
	}
	// The token proves the opener is signed in (or holds the share), so the
	// page may also run full-tab ("Open original").
	serveRawFile(w, withTopLevelScripts(r.WithContext(withScope(r.Context(), sc))), rel)
}

// handleOwnerPreview serves /_preview/<token>/<path> for the owner.
func (s *server) handleOwnerPreview(w http.ResponseWriter, r *http.Request) {
	tok, rel, ok := splitPreview(strings.TrimPrefix(r.URL.Path, previewPrefix))
	if !ok || !s.checkPreviewToken(tok, "", s.owner.fp) {
		http.Error(w, "preview link expired; reload the page", http.StatusUnauthorized)
		return
	}
	servePreview(w, r, scope{root: s.root}, rel)
}
