package html

import (
	"bytes"
	stdhtml "html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// artifactMarkdown is deliberately the existing server-side Goldmark path,
// not a second browser parser. GFM supplies tables, task lists, autolinks and
// strikethrough. The safe renderer exposes authored HTML as escaped text,
// rejects dangerous link destinations, and prevents images from causing an
// automatic network request when somebody opens the offline dashboard.
var artifactMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(
		util.Prioritized(artifactSafeRenderer{}, 100),
	)),
)

func renderArtifactMarkdown(source string) (string, error) {
	var out bytes.Buffer
	if err := artifactMarkdown.Convert([]byte(source), &out); err != nil {
		return "", err
	}
	return out.String(), nil
}

// artifactSafeRenderer keeps authored markup readable without ever turning it
// into executable DOM. Images are the one Markdown construct that can contact
// an artifact-controlled URL without a deliberate click, so only their alt
// text remains.
type artifactSafeRenderer struct{}

func (artifactSafeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkSkipChildren, nil
		}
		var altText strings.Builder
		if err := ast.Walk(node, func(child ast.Node, childEntering bool) (ast.WalkStatus, error) {
			if !childEntering {
				return ast.WalkContinue, nil
			}
			switch value := child.(type) {
			case *ast.Text:
				altText.Write(value.Value(source))
			case *ast.String:
				altText.Write(value.Value)
			}
			return ast.WalkContinue, nil
		}); err != nil {
			return ast.WalkStop, err
		}
		alt := stdhtml.EscapeString(altText.String())
		if _, err := w.WriteString(`<span class="artifact-image-alt" role="img">` + alt + `</span>`); err != nil {
			return ast.WalkStop, err
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(ast.KindRawHTML, renderArtifactRawMarkup)
	reg.Register(ast.KindHTMLBlock, renderArtifactRawMarkup)
}

func renderArtifactRawMarkup(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var raw []byte
	switch value := node.(type) {
	case *ast.RawHTML:
		raw = value.Segments.Value(source)
	case *ast.HTMLBlock:
		raw = value.Lines().Value(source)
		if value.HasClosure() {
			raw = append(raw, value.ClosureLine.Value(source)...)
		}
	}
	if _, err := w.WriteString(stdhtml.EscapeString(string(raw))); err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, nil
}
