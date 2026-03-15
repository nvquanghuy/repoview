# Changelog

## [Unreleased]

- [added] Windows support: release pipeline now builds Windows binaries (amd64/arm64) as .zip archives
- [added] PowerShell install script (`install.ps1`) for Windows one-liner installation
- [fixed] Path separators normalized to forward slashes in all API responses, fixing frontend on Windows
- [fixed] WebSocket file-change notifications now work correctly on Windows (path matching uses forward slashes)
- [fixed] Markdown frontmatter parsing now handles CRLF line endings (common on Windows)
- [fixed] Self-update uses pure Go archive extraction instead of shelling out to `sh`/`tar`, enabling updates on Windows
- [changed] VS Code editor setup URL now links to generic CLI docs instead of macOS-specific page

## [v0.5.1] - 2026-03-15

- [changed] Default port changed from 8080 to 7777 (less commonly used by other services)

## [v0.5.0] - 2026-03-06

- [added] Add "Edit" button with editor selection, open local computer code editors.

## [v0.4.0] - 2026-03-01

- [fixed] Live reload now works when files are added/removed in expanded folders. Fixed two issues: (1) backend now notifies clients watching parent directories, not just exact paths; (2) frontend now re-fetches all expanded directories during refresh so they stay expanded.
- [changed] Improved error messages: "Connection lost" banner now only shows for actual network failures. HTTP errors (e.g., file not found) show specific inline messages instead of the misleading connection error.
- [added] Outline sidebar for markdown files: click "Outline" button (next to Preview/Code toggle) to open a right sidebar with document headings. State persists in localStorage.
- [added] Clickable anchor links on markdown headings: hover over any heading to see the link icon, click to copy the URL with hash. Outline links also update the URL hash.

## [v0.3.1] - 2026-03-01

- [changed] Release script now includes changelog in git tag messages, making them visible on GitHub's tags page

## [v0.3.0] - 2026-03-01

- [added] Add update command and auto-update check
  - `repoview update` command to download and install the latest version
  - `repoview update --check` to check for updates without installing
  - Background update check on startup (at most once per day) with notification when update available
  - Update command shows release notes and link to GitHub release page

## [v0.2.0] - 2026-03-01
- [changed] Folder click behavior: clicking a non-selected folder navigates to it; clicking an already-selected folder toggles expand/collapse. Caret always toggles.
- [added] Active folder highlighting in sidebar when viewing a directory
- [added] Automatically find another port if default port is not available.
- [added] Add connection error banner when API fails

## [v0.1.0] - 2026-03-01

- [changed] Treeview: Clicking on folder icon also trigger collapse/expand of items. This allows easier toggle (wider surface click area).
- [added] CSV row-to-record navigation: click the arrow icon (→) in any table row to jump directly to that record in Records view. The URL updates to `?view=records&r=N` for easy sharing.
- [added] CSV Records view mode: toggle between Table and Records views for CSV files. Records view displays data as vertical cards, making wide CSV files easier to read. URL supports `?view=records` and `?view=records&r=N` for direct linking to specific records.
- [changed] Moved view toggle (Preview/Code, Table/Records) from breadcrumb bar to a new file header bar below the breadcrumb, following GitHub's layout. The file header bar also displays file metadata like size and MIME type for binary files.

## [v0.0.1] - 2026-02-28

- [added] README.md is rendered below directory listings (like GitHub) when present in a directory
- [fixed] Sidebar now scrolls to the active file when using "Go to file" search or navigating to deeply nested files
- [added] SVG files now have a Preview/Code toggle like markdown: preview renders the image inline, code view shows syntax-highlighted SVG source
- [added] Binary file handling: images display inline with preview, non-image binaries show metadata panel with file size, MIME type, and download link
- [added] `/api/raw` endpoint for serving raw file bytes (used for inline `<img>`, `<video>`, `<audio>` tags and downloads)
- [added] Binary detection via `net/http.DetectContentType` — prevents browser hang when opening large binary files
- [changed] Moved "Go to file" button from breadcrumb bar to sidebar, above the file tree, where it's closer to the files it searches
- [added] Markdown raw/preview toggle: switch between rendered preview and syntax-highlighted source code, like GitHub's "Code" / "Preview" tabs
- [added] View state persists in URL via `?view=code` query param (shareable and survives refresh)
- [fixed] Clicking the chevron icon on an expanded folder now collapses it (chevron toggles open/closed, clicking the folder name still navigates)
- [fixed] Fuzzy search now treats spaces as term separators (like GitHub/VSCode): typing "nes agg" matches "nested_aggregation.go" by matching each term independently
- [added] `--host` flag to control bind address (default `127.0.0.1` instead of `0.0.0.0` for safer localhost-only access)
- [fixed] Browser now opens `http://localhost:<port>` instead of `http://0.0.0.0:<port>` which isn't browsable on all OSes
- [added] GitHub Actions release workflow: cross-compiles for macOS and Linux (amd64/arm64) on `v*` tags and uploads tarballs to GitHub Releases
- [added] One-liner install script (`install.sh`) for teammates: detects OS/arch, downloads the right binary, installs to `~/.local/bin`
- [added] Tag-based versioning with ldflags injection and `release.sh` script
- [changed] Show dotfiles and dotfolders (e.g. `.github`, `.gitignore`) in the sidebar file tree
- [added] Pretty-print support for JSON and YAML files: minified/compact files are automatically formatted with proper indentation before syntax highlighting. Gracefully falls back to raw content if parsing fails.
- [added] Dynamic page title: shows `filename - RepoView` when viewing a file, `dirname - RepoView` for subdirectories, and `RepoView` at root
- [added] Playwright browser test suite (`tests/`) with 10 end-to-end tests covering sidebar tree, file viewing (markdown, CSV, code), frontmatter rendering, directory listing, parent navigation, fuzzy search, anchor links, and URL routing
- [added] Single-binary Go server with embedded static files (`go:embed`)
- [added] GitHub-style UI with resizable sidebar and breadcrumb navigation
- [added] Lazy-loading file tree via `/api/tree` endpoint
- [added] Markdown rendering with GitHub-Flavored Markdown (goldmark + GFM extensions)
- [added] CSV rendering to styled HTML tables
- [added] Fuzzy file search modal (press `t`) with custom fuzzysort implementation
- [added] WebSocket-based live reload with fsnotify file watching
- [added] Smart watcher: only watches expanded directories and the currently viewed file
- [added] Flat file listing via `/api/files` (uses `git ls-files` when available)
- [added] Auto-open browser on startup (`-no-browser` flag to disable)
- [fixed] `/api/file` endpoint now returns JSON (`content`, `name`, `path`, `isMarkdown`, `isCSV`) instead of raw HTML, matching the frontend's expected API contract
- [tests] Comprehensive backend test suite (`main_test.go`) with 14 tests covering tree, file, files, safePath, WebSocket, and static file serving
- [tests] Frontend test harness (`static/index_test.html`) covering `esc()`, `escAttr()`, `fuzzysort()`, JSON parsing, keyboard shortcuts, and live API calls
- [tests] Test data files (`testdata/`) for markdown, CSV, plain text, and nested directory scenarios
