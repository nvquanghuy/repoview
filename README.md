# RepoView

A lightweight, single-binary local folder viewer that replicates the GitHub UI for browsing files, rendering Markdown, and viewing CSV data.

![repoview-demo](https://github.com/user-attachments/assets/940a295f-bbc4-4949-b1a1-770f39781b2c)

## Why

I noticed my marketing team, when working with Claude Code and lots of markdown files, pushing commits to GitHub just to read them with a nice UI. That seemed backwards — why should you need to commit and push just to preview your docs? RepoView gives you that same GitHub reading experience locally, instantly.

## Features

- **GitHub-style UI** — familiar layout with resizable sidebar, breadcrumb navigation, and file tree
- **Markdown rendering** — GitHub-Flavored Markdown via goldmark (tables, task lists, code blocks)
- **CSV rendering** — automatic conversion to styled HTML tables
- **Fuzzy file search** — press `t` to open a "Go to file" modal with fuzzy matching
- **Full-text search** — press `/` (or tap the search icon) to search inside every file; all words must match, `"quoted phrases"` match exactly, and opening a result scrolls to the first hit. `Tab` switches between file and content search
- **Backlinks** — markdown notes list the notes that link to them (`[[wiki links]]`, `![[embeds]]`, and relative `[text](note.md)` links), most recently edited first; tap one to jump to the link
- **Live reload** — WebSocket-based file watching with fsnotify; changes appear instantly
- **Single binary** — all HTML/CSS/JS embedded via `go:embed`, no external dependencies at runtime
- **Lazy-loading tree** — sidebar fetches directory contents on demand for fast startup on large repos
- **Auto-update** — checks for updates daily and can self-update with `repoview update`

## Install

### One-liner (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/nvquanghuy/repoview/master/install.sh | bash
```

Downloads the latest release binary for your OS/architecture to `~/.local/bin`.

### One-liner (Windows PowerShell)

```powershell
irm https://raw.githubusercontent.com/nvquanghuy/repoview/master/install.ps1 | iex
```

Downloads the latest release binary to `%LOCALAPPDATA%\Programs\repoview`.

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
cd ~/projects/myrepo && repoview
```

This starts a local server at http://localhost:7777 and opens your browser to browse the repo with a GitHub-style UI.

### Options

```bash
repoview                    # serve current directory on http://localhost:7777
repoview /path/to/dir       # serve a specific directory
repoview --port 3000        # use a custom port
repoview --host 0.0.0.0     # expose to the network (default: 127.0.0.1)
repoview --no-browser       # don't auto-open the browser
repoview --version          # print version and exit
repoview update             # download and install the latest version
repoview update --check     # check for updates without installing
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | `127.0.0.1` | Host/IP to bind to |
| `--port` | `7777` | Port to serve on |
| `--no-browser` | `false` | Don't auto-open the browser on startup |
| `--version` | | Print version and exit |

### Updating

RepoView checks for updates automatically once per day on startup. If a new version is available, you'll see a notification in the terminal.

To manually update:

```bash
repoview update           # download and install the latest version
repoview update --check   # check for updates without installing
```

The update command downloads the latest release from GitHub, shows release notes, and replaces the current binary.

### Using on your phone (Add to Home Screen)

RepoView can be installed as a home-screen web app (PWA), so a server on another machine becomes a notes viewer on your phone. Nothing is synced or stored on the device. RepoView has no authentication, so keep it on a private network. [Tailscale](https://tailscale.com) works well:

```bash
# on the server (e.g. a VPS syncing your notes from Dropbox)
repoview --host "$(tailscale ip -4)" --no-browser ~/Dropbox/notes
```

On the iPhone (with Tailscale connected), open `http://<tailscale-ip>:7777` in Safari, then tap **Share → Add to Home Screen**. It launches full-screen with its own icon, named after the folder.

## API

| Endpoint | Description |
|----------|-------------|
| `GET /api/tree?path=` | Returns immediate children of a directory as JSON |
| `GET /api/file?path=` | Returns rendered file content as JSON (`content`, `name`, `path`, `isMarkdown`, `isCSV`) |
| `GET /api/files` | Returns a flat list of all file paths (uses `git ls-files` when available) |
| `GET /api/search?q=&limit=` | Case-insensitive full-text search over text files (≤2 MB). Every term must appear in the file's contents or path; `"quoted phrases"` are matched exactly. Returns ranked results with up to 3 matching lines each |
| `GET /api/manifest.webmanifest` | Web app manifest (enables "Add to Home Screen") |
| `GET /api/backlinks?path=` | Markdown notes linking to `path`, most recently modified first, with up to 3 linking lines each. Counts wiki links (in Obsidian vaults) and relative/root-relative markdown links; ignores code blocks |
| `WS /ws` | WebSocket for file change notifications |

## Development

```bash
# Run locally
go run . /path/to/dir

# Run tests
go test -v ./...

# Build
go build -o repoview .
```

### Releasing

```bash
./release.sh          # bump patch: v0.1.0 → v0.1.1
./release.sh minor    # bump minor: v0.1.0 → v0.2.0
./release.sh major    # bump major: v0.1.0 → v1.0.0
./release.sh 2.0.0    # explicit version
```

This updates `CHANGELOG.md`, commits, tags, and pushes. GitHub Actions builds the release binaries.

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
