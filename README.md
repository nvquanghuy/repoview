# RepoView

A lightweight, single-binary local folder viewer that replicates the GitHub UI for browsing files, rendering Markdown, and viewing CSV data.

![repoview-demo](https://github.com/user-attachments/assets/940a295f-bbc4-4949-b1a1-770f39781b2c)


## Features

- **GitHub-style UI** — familiar layout with resizable sidebar, breadcrumb navigation, and file tree
- **Markdown rendering** — GitHub-Flavored Markdown via goldmark (tables, task lists, code blocks)
- **CSV rendering** — automatic conversion to styled HTML tables
- **Fuzzy file search** — press `t` to open a "Go to file" modal with fuzzy matching
- **Live reload** — WebSocket-based file watching with fsnotify; changes appear instantly
- **Single binary** — all HTML/CSS/JS embedded via `go:embed`, no external dependencies at runtime
- **Lazy-loading tree** — sidebar fetches directory contents on demand for fast startup on large repos

## Install

### One-liner (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/nvquanghuy/repoview/master/install.sh | bash
```

Downloads the latest release binary for your OS/architecture to `~/.local/bin`.

### From source

```bash
go install github.com/nvquanghuy/repoview@latest
```

Or build locally:

```bash
go build -o repoview .
```

## Usage

```bash
repoview                    # serve current directory on http://localhost:8080
repoview /path/to/dir       # serve a specific directory
repoview --port 3000        # use a custom port
repoview --host 0.0.0.0     # expose to the network (default: 127.0.0.1)
repoview --no-browser       # don't auto-open the browser
repoview --version          # print version and exit
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | `127.0.0.1` | Host/IP to bind to |
| `--port` | `8080` | Port to serve on |
| `--no-browser` | `false` | Don't auto-open the browser on startup |
| `--version` | | Print version and exit |

## API

| Endpoint | Description |
|----------|-------------|
| `GET /api/tree?path=` | Returns immediate children of a directory as JSON |
| `GET /api/file?path=` | Returns rendered file content as JSON (`content`, `name`, `path`, `isMarkdown`, `isCSV`) |
| `GET /api/files` | Returns a flat list of all file paths (uses `git ls-files` when available) |
| `WS /ws` | WebSocket for file change notifications |

## Development

```bash
# Run tests
go test -v ./...

# Build
go build -o repoview .
```

### Project Structure

```
main.go              # Server, API handlers, WebSocket hub, file watchers
static/index.html    # Single-page frontend (embedded at build time)
static/index_test.html # Frontend test harness
main_test.go         # Backend unit and integration tests
testdata/            # Sample files used by tests
```

## License

MIT
