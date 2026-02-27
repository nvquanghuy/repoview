**System Goal:** Create `repoview`, a high-performance local folder viewer in Go. It must replicate the GitHub UI for Markdown and CSV files.
**Core Architecture:**
1. **Single Binary:** Use `go:embed` for all HTML/CSS/JS. Use `pkg/browser` to auto-open the browser on startup.
2. **Lazy-Loading Sidebar:** >    - Backend: `/api/tree?path=...` returns only immediate children (JSON).
* Frontend: A stateful sidebar that fetches and appends children when a folder is expanded.


3. **Smart Watcher:** Use `fsnotify`. Only watch the currently viewed file and the directories currently expanded in the sidebar. Stop watching paths when they are collapsed or closed.
4. **Fuzzy File Jumper:**
* On startup, the backend crawls the directory (use `git ls-files` if in a git repo for speed) to create a flat list of all file paths.
* Frontend: Implement a "Go to file" modal (shortcut `t`) using a fuzzy search library (e.g., `fuzzysort`).


5. **Renderers:**
* **Markdown:** Use `goldmark` with GitHub-Flavored Markdown extensions.
* **CSV:** Convert CSV to a GitHub-style HTML `<table>`.



**UI Requirements (Referencing GitHub):**
* **Layout:** Fixed-width resizable left sidebar; main content area with sticky breadcrumb header.
* **Icons:** Use a simple SVG icon set (like Octicons) for folders and files.
* **Styling:** Match GitHub’s light theme (System fonts: `-apple-system, BlinkMacSystemFont, "Segoe UI"`, specific border colors `#d0d7de`, and white backgrounds).


**Implementation Steps:**
1. Create a `main.go` that handles the server, auto-opening the browser, and the lazy-load API.
2. Implement the "Smart Watcher" logic to manage `fsnotify` subscriptions.
3. Create a single-page `index.html` with Vanilla JS to handle the sidebar state, the fuzzy search modal, and the WebSocket for live-reloading.

