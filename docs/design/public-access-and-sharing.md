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
cross-site websockets), and untrusted files inside the served folder (a
cloned repo, downloaded docs).

| Control | Detail and rationale |
|---------|----------------------|
| Server-side scope | Every path goes through `scope.resolve`; nothing trusts the frontend. |
| CSRF | State-changing requests (`POST /login`, `/logout`, share login, `/api/shares`, `/api/open`) require `Origin` matching `Host` (or `Sec-Fetch-Site: same-origin/none`); `Origin: null` is refused because sandboxed previews send it. |
| Referrer-Policy `same-origin` | Share URLs never leak to other sites via `Referer`. Not `no-referrer`: that makes Chromium send `Origin: null` on our own form posts, which the CSRF check refuses. |
| Websocket Origin check | Browsers attach cookies to cross-site websocket handshakes. |
| Raw content sandbox | `/_raw/` and `/api/raw` add `Content-Security-Policy: sandbox allow-scripts` to any response whose **final** type can run script (HTML, SVG, any XML/XSL), including files sniffed as HTML. A script-bearing file in the folder therefore never runs with the owner's cookies (otherwise it could create a passwordless share of everything). PDFs are left unsandboxed because viewers refuse to render in a sandbox. SVG previews in the SPA use `<img>`, never inline markup. |
| Caching | `Cache-Control: no-store` on everything except icons, so Cloudflare never caches private pages (it caches by file extension, and SPA URLs end in `.md`, `.pdf`, …). |
| Rate limiting | Attempts are reserved *before* the bcrypt check (parallel guesses can't race past). Limits: 5 per client (IPv6 grouped by /64) per login, 50 per login across all clients, both per 15 min; at most 4 concurrent bcrypt checks. Locked-out keys survive a flood of new keys. The per-login cap means someone hammering the owner login can block new owner sign-ins for 15 minutes; existing sessions keep working. |
| Proxy headers | `Cf-Connecting-IP` and `X-Forwarded-Proto` are trusted only from a loopback peer (cloudflared on the same machine). `X-Forwarded-For` is ignored: other local proxies pass client values through. |
| Redirects | `next` must be a single-slash local path without backslashes or control characters (`/\t/evil.com` would become `//evil.com`). |
| Other headers | `nosniff`, `X-Frame-Options: SAMEORIGIN`, `X-Robots-Tag: noindex`. |
| Env scrubbing | The password variables are removed from the environment at startup so editors launched via `/api/open` don't inherit them. |
| Revoked shares' websockets | Closed by a sweep every 30 s and on the next file event; validity cached 2 s. |

An independent review probed path tricks (encoded `..`, backslashes,
symlinks, dotfiles, `/api/../`), session confusion and the items above;
the findings it confirmed are fixed and covered by tests.

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
- Only one proxy setup is supported for client IPs: cloudflared on the same
  machine.
- Share passwords can't be changed; revoke and re-share.

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
- `tests/sharing.spec.ts` (Playwright, project `auth`, port 3124): login,
  folder/file shares in a fresh browser context, revoke, logout.
