package main

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// safeAutoLinks renders <scheme:...> autolinks like goldmark does, but drops
// the link when the URL is dangerous (javascript:, vbscript:, data: other
// than images). goldmark applies that check to [text](url) links and images
// but not to autolinks.
type safeAutoLinks struct{}

func (safeAutoLinks) Extend(m goldmark.Markdown) {
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(safeAutoLinks{}, 100), // runs ahead of the default renderer (1000)
	))
}

func (safeAutoLinks) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindAutoLink, renderSafeAutoLink)
}

func renderSafeAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	url, label := n.URL(source), n.Label(source)
	if n.AutoLinkType == ast.AutoLinkEmail && !bytes.HasPrefix(bytes.ToLower(url), []byte("mailto:")) {
		url = append([]byte("mailto:"), url...)
	}
	if gmhtml.IsDangerousURL(url) {
		w.Write(util.EscapeHTML(label)) // keep the text, lose the link
		return ast.WalkContinue, nil
	}
	w.WriteString(`<a href="`)
	w.Write(util.EscapeHTML(util.URLEscape(url, false)))
	w.WriteString(`">`)
	w.Write(util.EscapeHTML(label))
	w.WriteString(`</a>`)
	return ast.WalkContinue, nil
}
