# Public access and share links

Status: shipped (unreleased at time of writing). Owner: repo maintainer.

This document records why password login and share links work the way they
do, so later changes keep the same security properties. For how to use the
feature, see the README ("Public access and sharing").

## Goals

1. Run RepoView on the public internet so the owner can browse their files
   from anywhere, behind a password.
2. Share specific files or folders with colleagues, each link with its own
   optional password and an expiry, without giving them access to anything
   else.
3. Change nothing for the existing local use (no password set → no login,
   no sharing, same URLs).
4. Several RepoView processes on one machine must not interfere with each
   other.

Non-goals: user accounts, per-person permissions, write access, TLS
termination (a proxy does that), HTML sanitising of note content (see
"Known limitations").

## Decisions

These were settled one by one before implementation.

| # | Question | Decision | Why |
|---|----------|----------|-----|
| 1 | How is it exposed? | Behind Cloudflare Tunnel; RepoView stays plain HTTP on localhost. | Certificates, DDoS protection and rate limiting at the edge are the proxy's job; RepoView stays small. |
| 2 | Owner login | `REPOVIEW_PASSWORD` (or a bcrypt `REPOVIEW_PASSWORD_HASH`), login page, signed cookie valid 30 days. Unset → today's behaviour. | Self-contained; no external identity provider; opt-in. |
| 3 | Managing shares | In the UI: a Share button on the current file/folder, a panel to list and revoke. Stored in a JSON file. | Sharing happens while looking at the thing; the file stays readable and editable by hand. |
| 4 | Link and password model | Each share has its own unguessable link and its own optional password and expiry (presets or custom hours/days, or never). The link carries a cosmetic slug: `/s/<slug>-<22-char id>`. | Per-colleague revocation; the slug tells people what the link is about, the id alone grants nothing without the share record. |
| 5 | What visitors get | The full viewer, confined to the shared item. Folder share: tree, search, backlinks, raw, live reload, all scoped. File share: just the file, no sidebar. Owner-only features are absent. | Same experience for colleagues; the boundary is enforced server-side on every request. |
| 6 | Brute force and sessions | 5 attempts per client per login per 15 min; share sessions end with the share (max 30 days); a random signing key in the config dir; revocation and expiry take effect immediately; changing the owner password ends owner sessions. | Bounded guessing, no stale access. |
| 7 | Where state lives | `~/.config/repoview/` (`$REPOVIEW_CONFIG_DIR` overrides): `secret` and `shares/<instance>.json`. | Password hashes never sit in the served (possibly git-pushed) folder. |
| 8 | Multiple instances | Per-root share files, cross-process file locks, atomic writes, cookies and tokens bound to the served root. | Running repoview on several folders at once must just work. |

## Architecture

Functional core, imperative shell:

| File | Role |
|------|------|
| `scope.go` | **Core + thin shell.** `scope` = what a request may see (`root`, optional single file `only`, `restricted` flag for shares). `checkRel` is the pure path decision; `resolve` adds the symlink check. Every handler resolves paths via `scopeOf(r)`. |
| `auth.go` | **Pure core.** HMAC session tokens, owner credential, rate limiter (clock injected), share ids/slugs/expiry, request facts (`clientIP`, `isHTTPS`, `sameOrigin`, `safeNext`, `rateKey`). |
| `raw.go` | `scriptableType` (pure) and `sandboxWriter`, which sandboxes raw responses by their final Content-Type. |
| `sharestore.go` | **Shell.** Share persistence and the signing secret. |
| `gate.go` | **Shell.** Route assembly, sessions, password checks, share area, shares API. |
| `pages.go` | Server-rendered login / share-password / error pages and `renderIndex` (injects `var RV = {...}` into the SPA). |
| `static/index.html` | Reads `RV.base` / `RV.mode` / `RV.file`; prefixes every API, raw, websocket and page URL with `RV.base`; owner-only UI hidden unless `RV.mode === "owner"`. |

### Routing

```
local mode (no password)           password mode
------------------------           -------------------------------------------
app (owner mux)                    securityHeaders(public mux)
  /api/*   read-only API (scoped)    /login, /logout          login pages
  /_raw/*  raw bytes (scoped)        /s/<ref>/...             share area
  /api/editors, /api/open            /api/icons/*             public icons
  /ws                                /  → requireOwner(app)   everything else
  /        SPA                                                  incl. /api/shares
```

`apiHandler()` (in `scope.go`) is the **only** set of endpoints share
visitors can reach: tree, file, files, search, backlinks, raw, jsonl,
`/_raw/`. Anything owner-only (`/api/editors`, `/api/open`, `/api/shares`,
the manifest) is registered on the owner mux and is therefore 404 inside a
share. **Adding an endpoint:** put it in `apiHandler()` only if share
visitors may use it, and resolve paths with `scopeOf(r).resolve(...)`, never
`rootDir`.

### Share area: `/s/<slug>-<id>/<sub>`

1. Parse the id from the last 22 characters (slug ignored); look the share up
   (fresh read of the store); unknown, expired or revoked → 404 page.
2. `/s/<ref>` → 301 to `/s/<ref>/`.
3. `POST /s/<ref>/login` → rate-limited password check → share cookie.
4. Locked (password set, no valid share cookie, no owner session) → API
   paths 401, pages show the password prompt.
5. Shared path missing on disk → 404.
6. Otherwise the request gets the share's scope: `ws` (scoped websocket),
   `api/*` and `_raw/*` (rewritten onto `apiHandler`), anything else the SPA
   with `RV = {base: "/s/<ref>", mode: "share", file}`.

The owner's session also unlocks shares (preview without the password).

### One-click links and invites

Sending a link and a password as two things is friction, so the Share dialog
offers two shortcuts for password shares (the password exists in plaintext
only in that dialog, so both are offered only right after creating a share):

- **One-click link:** `/s/<ref>#k=<url-encoded password>`. Browsers never
  send the fragment to the server, so the key stays out of Cloudflare and
  server logs, link-preview bots (they see the prompt) and `Referer`. The
  prompt page's inline script removes the fragment with
  `history.replaceState` and then submits the key through the normal form,
  so it gets the same rate limit, CSRF check and cookie as a typed password.
  The script is left out of pages that report an error, so a wrong key shows
  "Incorrect password." once instead of looping. The SPA strips `#k=` too, for
  visitors who are already unlocked.
- **Copy link + password:** one clipboard message (item, link, password) for
  when you'd rather the password travel separately or with the link.

Trade-off: a one-click link is a bearer link, like a share without a
password, minus the log and preview leaks. Anyone it's forwarded to gets in.
Send link and password over different channels when that matters. The
browser's own history may still record the URL as it was typed or clicked.

### Scopes

| | owner | folder share | file share |
|-|-------|--------------|------------|
| root | served folder | shared folder | the file's folder |
| `only` | – | – | the file |
| dot-paths (`.env`, `.git/`) | visible | hidden | hidden |
| symlinks leaving the root | followed (raw: refused) | refused | refused |
| `..` in a request | refused | refused | refused |

The websocket hub sends each client paths relative to its own scope and
drops events outside it.

### Sessions

Token = `base64url(json claims) . base64url(HMAC-SHA256(secret, payload))`.
Claims: kind (`owner`/`share`), instance id (hash of the served root), share
id, credential fingerprint, expiry.

- Owner fingerprint: keyed hash of the plaintext password (stable across
  restarts, unlike a fresh bcrypt salt), or a hash of the configured bcrypt
  hash. Changing the password invalidates every owner session.
- Share fingerprint: hash of id + password hash.
- Cookies: `rv_owner_<instance>` (Path `/`), `rv_share_<id>` (Path `/s/`);
  `HttpOnly`, `SameSite=Lax`, `Secure` when the request came over HTTPS.
- The instance id in both the cookie name and the claims means a cookie from
  one served folder is useless on another, even though all instances share
  the signing key.
- Logout clears the cookie on that device only (stateless tokens); ending all
  sessions = changing the password.

### HTML previews (capability URLs)

HTML files preview in an iframe with `sandbox="allow-scripts"` (and a CSP
sandbox), which gives the page an opaque origin. Browsers then treat its
requests as cross-site and don't send `SameSite=Lax` cookies, so relative
images and stylesheets inside the page would get 401 for the owner and in
password shares. Instead, when login is on, `/api/file` returns a
`previewURL` such as `/_preview/<token>/site/index.html` (or
`/s/<ref>/_preview/<token>/…`), and relative URLs in the page resolve under
the same token, so they need no cookies (`preview.go`).

- The token is a signed `preview` claim bound to the instance and to the
  owner's or the share's credential fingerprint, valid 12 hours. Changing
  the owner password, revoking the share or its expiry kills it; it is never
  accepted as a session, nor on another share or the owner area.
- It grants read access to the same scope the viewer already had, through
  the raw file route only.
- Scriptable responses carry `connect-src 'none'`, so a malicious page's
  scripts can't use the token in their own URL to read other files.
- Without a password there are no cookies to lose, so the preview loads from
  `/_raw/` directly.
- "Open original" opens the same `previewURL` in a new tab. Opened top-level,
  a preview URL keeps `sandbox allow-scripts; connect-src 'none'` (scripts
  run, opaque origin) instead of the plain `sandbox` that `/_raw/` gets: only
  a signed-in owner or a share holder can obtain the token, so an outsider
  can't send a victim a link that shows a planted page as a script-driven
  fake login form. Local mode (no password, no login form) allows the same on
  `/_raw/` (`allowTopLevelScripts` in `gate.go`).

### Share storage and concurrency

`shares/<instanceID(root)>.json` holds `{root, shares[]}`; the root is checked
on read so a stray file can't be applied to the wrong folder. Writes take a
cross-process `flock` on `<file>.lock`, re-read, mutate, then write a temp
file and rename it into place (readers never see partial JSON). Reads happen
per request, so a revoke from any process is effective immediately. A corrupt
file is an error, never silently replaced. The secret is created by writing a
temp file and hard-linking it into place, so concurrent first starts agree on
one key. Files are `0600`, directories `0700`.

## Security model

Threats considered: anonymous internet users, share visitors trying to
reach beyond their share, malicious web pages the owner visits (CSRF,
cross-site websockets), untrusted files inside the served folder (a cloned
repo, downloaded docs, a synced `.git` directory), and resource exhaustion.

| Control | Detail and rationale |
|---------|----------------------|
| Server-side scope | Every path goes through `scope.resolve`; nothing trusts the frontend. |
| CSRF | State-changing requests (`POST /login`, `/logout`, share login, `/api/shares`, `/api/open`) require `Origin` matching `Host` (or `Sec-Fetch-Site: same-origin/none`); `Origin: null` is refused because sandboxed previews send it. |
| Referrer-Policy `same-origin` | Share URLs never leak to other sites via `Referer`. Not `no-referrer`: that makes Chromium send `Origin: null` on our own form posts, which the CSRF check refuses. |
| Websocket Origin check | Browsers attach cookies to cross-site websocket handshakes. |
| Raw content sandbox | `/_raw/` and `/api/raw` add a CSP sandbox to any response whose **final** type can run script (HTML, SVG, any XML/XSL), including files sniffed as HTML. In the preview iframe it is `sandbox allow-scripts` (opaque origin); opened as a top-level page (`Sec-Fetch-Dest: document`) it is plain `sandbox` (except through a preview capability URL or in local mode, see HTML previews), so a planted page can't draw a fake login form on our domain. A script-bearing file therefore never runs with the owner's cookies (otherwise it could create a passwordless share of everything). PDFs are left unsandboxed because viewers refuse to render in a sandbox. |
| Untrusted content in the SPA | SVG previews use `<img>`, never inline markup. Mermaid sources are re-rendered from text (`textContent`), never `innerHTML`; `securityLevel: 'strict'`. goldmark autolinks with `javascript:`/`vbscript:`/`data:` URLs lose their link (`markdown_safety.go`; goldmark only filters `[text](url)` links), and the SPA strips such URLs from every rendered markdown and `.docx` link as a second layer, using `URL` parsing so `java\tscript:` is caught. Index placeholders are filled in one pass so names can't splice values into the injected config. Third-party scripts (mermaid, mammoth) are pinned to exact versions with SRI. |
| Symlinked share roots | The shared item must resolve (after symlinks) inside the served root, at creation and on every visit, so a shared folder later replaced by a symlink (a pull, a sync) exposes nothing. |
| No repository-configured commands | Share visitors never cause `git` to run (their file lists come from a walk). The owner's `git ls-files` runs with `core.fsmonitor=false`, `core.untrackedCache=false` and no system config, so a planted `.git/config` can't execute commands. |
| Resource limits | `/api/file` renders text files of at most 2 MB (larger: metadata and a download; binary files over 2 MB are sniffed from their first 512 bytes and shown from `/api/raw`; a 20 MB source file took ~3.5 GB to highlight). At most 4 concurrent search/backlinks/JSONL scans. Websockets: 4 KB messages, only existing paths watched, ≤256 watches per client, ≤256 connections. Login forms ≤4 KB, read only after the origin check. HTTP server: 10 s header timeout, 2 min idle timeout, 64 KB headers. |
| Caching | `Cache-Control: no-store` on everything except icons, so Cloudflare never caches private pages (it caches by file extension, and SPA URLs end in `.md`, `.pdf`, …). |
| Rate limiting | Attempts are reserved *before* the bcrypt check (parallel guesses can't race past). Limits: 50 per login target (the owner login, or one share) across all clients, then 5 per client (IPv6 grouped by /64), both per 15 min; at most 4 concurrent bcrypt checks. Each target has its own per-client table, so flooding the owner login can't block share logins. Locked-out keys survive a flood of new keys. |
| Known devices | A successful owner login also sets `rv_device_<instance>` (Path `/login`, 1 year; a signed "this browser signed in before", not a session). It skips the per-login ceiling, so someone hammering the owner login can block sign-ins only from browsers you haven't used before; existing sessions keep working either way. |
| Proxy headers | `Cf-Connecting-IP` and `X-Forwarded-Proto` are trusted only from a loopback peer (cloudflared on the same machine). `X-Forwarded-For` is ignored: other local proxies pass client values through. |
| Redirects | `next` must be a single-slash local path without backslashes or control characters (`/\t/evil.com` would become `//evil.com`). |
| Other headers | `nosniff`, `X-Frame-Options: SAMEORIGIN`, `X-Robots-Tag: noindex`. |
| Env scrubbing | The password variables are removed from the environment at startup so editors launched via `/api/open` don't inherit them. |
| Revoked shares' websockets | Closed by a sweep every 30 s and on the next file event; validity cached 2 s. |
| Share prompt | Shows "Protected link", not the shared item's name. |
| Dependencies | Built with Go ≥ 1.26.11 (the `go` line in `go.mod`, which the release workflow uses) and goldmark ≥ 1.7.17; `govulncheck ./...` reports nothing. Re-run it before releases. |

Two rounds of independent review (white-box backend and frontend, and a
black-box pentest of a running instance behind a simulated tunnel) probed
path tricks (encoded `..`, backslashes, symlinks, dotfiles, `/api/../`,
81 paths × 6 methods), session confusion, XSS sinks and the items above.
Every confirmed finding is fixed and has a regression test.

## Known limitations / future work

- **Raw HTML in Obsidian notes.** In Obsidian vaults markdown is rendered
  with raw HTML enabled (pre-existing behaviour, needed for embeds), and the
  SPA inserts it into the page. A note containing e.g. `<img onerror=…>` runs
  script for whoever views it, owner included. Planned: sanitise the rendered
  HTML while keeping Obsidian formatting. Until then, don't serve vaults that
  contain notes from untrusted sources.
- No server-side session revocation (logout is per device; password change
  ends all owner sessions).
- Rate-limit state is per process and resets on restart.
- A narrow TOCTOU window remains between path checks and opening a file;
  exploiting it needs someone who can write into the served folder at that
  moment. (`os.Root` would close it.)
- Updates (`repoview update`) trust GitHub over TLS; there's no signature
  check.
- Only one proxy setup is supported for client IPs: cloudflared on the same
  machine.
- Share passwords can't be changed; revoke and re-share.

## Deploying on the internet: checklist

1. Run RepoView as your normal (unprivileged) user, bound to the default
   `127.0.0.1`, with cloudflared on the same machine.
2. Use a long random passphrase (e.g. 5+ random words). Prefer
   `REPOVIEW_PASSWORD_HASH` in a `0600` environment file (systemd
   `EnvironmentFile=`) over a plaintext variable in shell history.
3. Don't add Cloudflare cache rules that override origin headers ("Cache
   Everything" with an Edge TTL): RepoView marks every private response
   `no-store`, and such rules ignore that.
4. After the first login through the public hostname, check the
   `rv_owner_…` cookie is `Secure` (cloudflared should pass
   `X-Forwarded-Proto: https`).
5. Optional defense in depth: put a Cloudflare Access policy (your email)
   on everything except `/s/*` and `/api/icons/*`, so the owner area needs
   SSO before RepoView's own password, while share links stay public.
6. Don't serve folders that contain Obsidian notes, `.git` directories or
   HTML from sources you don't trust (see Known limitations).
7. Rebuild/update when Go or goldmark security releases land
   (`govulncheck ./...`).

## Tests

- `scope_test.go`: path decisions (pure), symlink/dotfile/single-file
  scoping, handlers and websocket notifications honouring the scope.
- `auth_test.go`: tokens, credentials, limiter (including concurrency and
  floods), request facts, ids/slugs/expiry.
- `sharestore_test.go`: CRUD, per-root isolation, concurrent writers from
  separate store instances, cross-process revoke, permissions, corrupt
  files, concurrent secret creation.
- `gate_test.go`: end-to-end over HTTP with an injectable clock
  (`newAuthEnv`): gate, login, CSRF, rate limits, expiry, other-instance
  cookies, password change, shares API, every share-visit path, `/_raw/`.
- `raw_test.go`: which content types are sandboxed.
- `hardening2_test.go` `TestSharePrompt_ReadsKeyFromFragment`: only the share
  prompt (and not its error page or the owner login) auto-submits `#k=`.
- `tests/sharing.spec.ts`: one-click link (unlock, key stripped from the URL,
  wrong key falls back to the prompt), copy link + password.
- `preview_test.go`: preview URLs for the owner and shares, relative assets
  without cookies, token scoping, password change, revocation, expiry.
- `render_security_test.go`: autolink script URLs, single-pass placeholders,
  top-level raw sandbox, local-mode headers.
- `hardening_test.go`, `hardening2_test.go`: symlinked share roots, git
  fsmonitor, large files, websocket limits, known devices, per-target
  limiters, bounded login bodies, share prompt, server timeouts, heavy-scan
  concurrency.
- `tests/repoview.spec.ts` "Rendering safety": mermaid theme switch, script
  URLs in links, too-large files.
- `tests/sharing.spec.ts` (Playwright, project `auth`, port 3124): login,
  folder/file shares in a fresh browser context, revoke, logout.
