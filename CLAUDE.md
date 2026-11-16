# Claude Code Guidelines

## Workflow
- **Always write tests** for new features and changes. Don't skip tests unless explicitly told to.
- Run tests before considering work complete.
- **Update CHANGELOG.md** when adding new features or making user-facing changes. Add entries under the `[Unreleased]` section.

## Build & Test
- Build: `go build -o repoview .`
- Install: `go install .` (places binary in `$GOBIN`, managed by mise)
- Test (backend): `go test -v ./...`
- Test (frontend/browser): `cd tests && npm test`

## Testing Patterns
- Use `setRoot(t, "testdata")` to set the root directory for tests.
- Use `newTestServer(t)` to create a server with full production routing (including `/files/*` SPA catch-all).
- Test fixtures live in `testdata/` (hello.md, data.csv, example.txt, subdir/nested.md).

## Project Structure
- Single Go binary with embedded static files (`static/index.html`).
- `main.go` — HTTP server, API handlers, WebSocket hub, file watcher.
- `main_test.go` — tests using `httptest`.
- `scope.go` — the per-request scope every handler resolves paths through (whole root for the owner, the shared item for share visitors). Use `scopeOf(r).resolve(path)`, never `rootDir` directly, in handlers.
- `auth.go` — pure core for login and sharing (tokens, credentials, rate limiter, share ids); `gate.go` — routing, sessions, shares API; `pages.go` — server-rendered login/prompt pages; `sharestore.go` — share and secret persistence (cross-process safe).
- Password mode tests: `newAuthEnv(t)` in `gate_test.go` serves a copy of testdata with a password, an injectable clock and a temp config dir.
- SPA routing: all non-API paths serve index.html; frontend uses History API (`pushState`/`popstate`) with clean URLs (e.g. `/readme.md`, `/src/main.go`).
