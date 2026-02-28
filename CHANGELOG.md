# Changelog

## [v22] - 2026-02-28

- [fixed] Sidebar now scrolls to the active file when using "Go to file" search or navigating to deeply nested files

## [v21] - 2026-02-28

- [added] SVG files now have a Preview/Code toggle like markdown: preview renders the image inline, code view shows syntax-highlighted SVG source

## [v20] - 2026-02-28

- [added] Binary file handling: images display inline with preview, non-image binaries show metadata panel with file size, MIME type, and download link
- [added] `/api/raw` endpoint for serving raw file bytes (used for inline `<img>`, `<video>`, `<audio>` tags and downloads)
- [added] Binary detection via `net/http.DetectContentType` — prevents browser hang when opening large binary files

## [v19] - 2026-02-28

- [changed] Moved "Go to file" button from breadcrumb bar to sidebar, above the file tree, where it's closer to the files it searches

## [v18] - 2026-02-28

- [added] Markdown raw/preview toggle: switch between rendered preview and syntax-highlighted source code, like GitHub's "Code" / "Preview" tabs
- [added] View state persists in URL via `?view=code` query param (shareable and survives refresh)

## [v17] - 2026-02-28

- [fixed] Clicking the chevron icon on an expanded folder now collapses it (chevron toggles open/closed, clicking the folder name still navigates)

## [v16] - 2026-02-28

- [fixed] Fuzzy search now treats spaces as term separators (like GitHub/VSCode): typing "nes agg" matches "nested_aggregation.go" by matching each term independently

## [v15] - 2026-02-28

- [added] `--host` flag to control bind address (default `127.0.0.1` instead of `0.0.0.0` for safer localhost-only access)
- [fixed] Browser now opens `http://localhost:<port>` instead of `http://0.0.0.0:<port>` which isn't browsable on all OSes

## [v14] - 2026-02-28

- [added] GitHub Actions release workflow: cross-compiles for macOS and Linux (amd64/arm64) on `v*` tags and uploads tarballs to GitHub Releases
- [added] One-liner install script (`install.sh`) for teammates: detects OS/arch, downloads the right binary, installs to `~/.local/bin`

## [v13] - 2026-02-27

- [changed] Show dotfiles and dotfolders (e.g. `.github`, `.gitignore`) in the sidebar file tree

## [v12] - 2026-02-27

- [added] Pretty-print support for JSON and YAML files: minified/compact files are automatically formatted with proper indentation before syntax highlighting. Gracefully falls back to raw content if parsing fails.

## [v11] - 2026-02-27

- [added] Dynamic page title: shows `filename - RepoView` when viewing a file, `dirname - RepoView` for subdirectories, and `RepoView` at root

## [v10] - 2026-02-27

- [added] Playwright browser test suite (`tests/`) with 10 end-to-end tests covering sidebar tree, file viewing (markdown, CSV, code), frontmatter rendering, directory listing, parent navigation, fuzzy search, anchor links, and URL routing

## [v1] - 2026-02-27

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
