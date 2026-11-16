package main

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"strings"
)

// renderIndex fills the placeholders in the embedded index.html. Values are
// JSON-encoded for <script> (encoding/json escapes <, > and &) and
// HTML-escaped for markup.
func renderIndex(raw []byte, rootName string, cfg pageConfig) []byte {
	if cfg.Mode == "" {
		cfg.Mode = "local"
	}
	var file any // null unless a single-file share
	if cfg.File != "" {
		file = cfg.File
	}
	jsCfg, _ := json.Marshal(map[string]any{"base": cfg.Base, "mode": cfg.Mode, "file": file})
	jsName, _ := json.Marshal(rootName)
	// One pass: inserted values are never rescanned, so a name containing a
	// placeholder can't splice more values into the page.
	return []byte(strings.NewReplacer(
		"__RV_CONFIG_JS__", string(jsCfg),
		"__ROOT_DIR_NAME_JS__", string(jsName),
		"__ROOT_DIR_NAME__", html.EscapeString(rootName),
	).Replace(string(raw)))
}

// pageData drives the small server-rendered pages (login, share password
// prompt, errors). They work without JavaScript.
type pageData struct {
	Title   string
	Heading string
	Message string
	Error   string
	Action  string // form target; empty = no form
	Next    string
	Button  string
	// KeyUnlock: submit the password from a one-click link's "#k=" fragment.
	// Never on a page that reports an error, so a bad key can't loop.
	KeyUnlock bool
}

// sharePromptPage doesn't name the shared item: that's for people who know
// the password.
func sharePromptPage(base string) pageData {
	return pageData{
		Title:     "Protected link",
		Heading:   "Protected link",
		Message:   "This link is password protected.",
		Action:    base + "/login",
		Button:    "View",
		KeyUnlock: true,
	}
}

func messagePage(heading, msg string) pageData {
	return pageData{Title: heading, Heading: heading, Message: msg}
}

func renderPage(w http.ResponseWriter, code int, p pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	pageTmpl.Execute(w, p)
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}} - RepoView</title>
<style>
  :root { --bg:#f6f8fa; --card:#fff; --fg:#1f2328; --muted:#59636e; --border:#d1d9e0; --accent:#1f883d; --accent-fg:#fff; --err:#d1242f; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#0d1117; --card:#151b23; --fg:#f0f6fc; --muted:#9198a1; --border:#3d444d; --accent:#238636; --accent-fg:#fff; --err:#f85149; }
  }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center; padding:16px;
         background:var(--bg); color:var(--fg); font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans",Helvetica,Arial,sans-serif; }
  main { width:100%; max-width:340px; background:var(--card); border:1px solid var(--border); border-radius:6px; padding:24px; }
  h1 { font-size:20px; font-weight:600; margin:0 0 8px; word-break:break-word; }
  p { margin:0 0 16px; color:var(--muted); }
  .error { color:var(--err); }
  label { display:block; font-weight:600; margin-bottom:6px; }
  input[type=password] { width:100%; padding:6px 10px; font:inherit; color:var(--fg); background:var(--bg);
         border:1px solid var(--border); border-radius:6px; margin-bottom:16px; }
  input[type=password]:focus { outline:2px solid #0969da; outline-offset:-1px; }
  button { width:100%; padding:6px 16px; font:inherit; font-weight:500; color:var(--accent-fg); background:var(--accent);
         border:1px solid rgba(31,35,40,.15); border-radius:6px; cursor:pointer; }
</style>
</head>
<body>
<main>
  <h1>{{.Heading}}</h1>
  {{if .Message}}<p>{{.Message}}</p>{{end}}
  {{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
  {{if .Action}}
  <form id="rv-form" method="post" action="{{.Action}}">
    {{if .Next}}<input type="hidden" name="next" value="{{.Next}}">{{end}}
    <label for="password">Password</label>
    <input type="password" id="password" name="password" autocomplete="current-password" autofocus required>
    <button type="submit">{{.Button}}</button>
  </form>
  {{if and .KeyUnlock (not .Error)}}
  <script id="rv-key-unlock">
  // One-click link: /s/<ref>#k=<password>. Browsers never send the fragment,
  // so the key stays out of server, proxy and link-preview logs. Drop it from
  // the address bar (and history) first, then submit it like a typed password.
  (function () {
    var m = /^#k=(.+)$/.exec(location.hash);
    if (!m) return;
    history.replaceState(null, "", location.pathname + location.search);
    var key;
    try { key = decodeURIComponent(m[1]); } catch (e) { return; }
    var f = document.getElementById("rv-form");
    f.elements.password.value = key;
    f.submit();
  })();
  </script>
  {{end}}
  {{end}}
</main>
</body>
</html>
`))
