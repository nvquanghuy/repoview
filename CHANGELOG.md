# Changelog

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
