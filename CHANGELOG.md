# Changelog

## [v13] - 2026-02-27

### Changed

- Show dotfiles and dotfolders (e.g. `.github`, `.gitignore`) in the sidebar file tree

## [v12] - 2026-02-27

### Added

- Pretty-print support for JSON and YAML files: minified/compact files are automatically formatted with proper indentation before syntax highlighting. Gracefully falls back to raw content if parsing fails.

## [v11] - 2026-02-27

### Added

- Dynamic page title: shows `filename - RepoView` when viewing a file, `dirname - RepoView` for subdirectories, and `RepoView` at root

## [v10] - 2026-02-27

### Added

- Playwright browser test suite (`tests/`) with 10 end-to-end tests covering sidebar tree, file viewing (markdown, CSV, code), frontmatter rendering, directory listing, parent navigation, fuzzy search, anchor links, and URL routing

## [v1] - 2026-02-27

### Added

- Single-binary Go server with embedded static files (`go:embed`)
- GitHub-style UI with resizable sidebar and breadcrumb navigation
- Lazy-loading file tree via `/api/tree` endpoint
- Markdown rendering with GitHub-Flavored Markdown (goldmark + GFM extensions)
- CSV rendering to styled HTML tables
- Fuzzy file search modal (press `t`) with custom fuzzysort implementation
- WebSocket-based live reload with fsnotify file watching
- Smart watcher: only watches expanded directories and the currently viewed file
- Flat file listing via `/api/files` (uses `git ls-files` when available)
- Auto-open browser on startup (`-no-browser` flag to disable)

### Fixed

- `/api/file` endpoint now returns JSON (`content`, `name`, `path`, `isMarkdown`, `isCSV`) instead of raw HTML, matching the frontend's expected API contract

### Added (Tests)

- Comprehensive backend test suite (`main_test.go`) with 14 tests covering tree, file, files, safePath, WebSocket, and static file serving
- Frontend test harness (`static/index_test.html`) covering `esc()`, `escAttr()`, `fuzzysort()`, JSON parsing, keyboard shortcuts, and live API calls
- Test data files (`testdata/`) for markdown, CSV, plain text, and nested directory scenarios
