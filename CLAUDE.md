# Claude Code Guidelines

## Workflow
- **Always write tests** for new features and changes. Don't skip tests unless explicitly told to.
- Run tests before considering work complete.

## Build & Test
- Build: `go build -o repoview .`
- Test: `go test -v ./...`

## Testing Patterns
- Use `setRoot(t, "testdata")` to set the root directory for tests.
- Use `newTestServer(t)` to create a server with full production routing (including `/files/*` SPA catch-all).
- Test fixtures live in `testdata/` (hello.md, data.csv, example.txt, subdir/nested.md).

## Project Structure
- Single Go binary with embedded static files (`static/index.html`).
- `main.go` — HTTP server, API handlers, WebSocket hub, file watcher.
- `main_test.go` — tests using `httptest`.
- SPA routing: all non-API paths serve index.html; frontend uses History API (`pushState`/`popstate`) with clean URLs (e.g. `/readme.md`, `/src/main.go`).
