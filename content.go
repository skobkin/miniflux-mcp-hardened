package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/JohannesKaufmann/dom"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"

	"miniflux.app/v2/client"
)

const contentFormatEnvironmentVariable = "MCP_CONTENT_FORMAT"

// contentFormat selects how article content is presented to MCP clients. The
// zero value is the safe default: untrusted feed HTML is converted to
// Markdown instead of crossing the boundary unmodified.
type contentFormat uint8

const (
	contentFormatMarkdown contentFormat = iota
	contentFormatHTML
)

func (f contentFormat) String() string {
	if f == contentFormatHTML {
		return "html"
	}

	return "markdown"
}

func parseContentFormat(value string) (contentFormat, error) {
	switch strings.TrimSpace(value) {
	case "":
		return contentFormatMarkdown, nil
	case contentFormatMarkdown.String():
		return contentFormatMarkdown, nil
	case contentFormatHTML.String():
		return contentFormatHTML, nil
	default:

		return 0, fmt.Errorf("unsupported %s %q (supported: %s, %s)", contentFormatEnvironmentVariable, value, contentFormatMarkdown, contentFormatHTML)
	}
}

// hardeningPlugin strips markup-based concealment that the base plugins do
// not handle: hidden elements, embedded/foreign content, and URL attributes
// whose scheme could trigger agent- or browser-side execution. It removes
// markup, never article text; censoring visible text is out of scope.
type hardeningPlugin struct{}

func newHardeningPlugin() converter.Plugin {
	return &hardeningPlugin{}
}

func (p *hardeningPlugin) Name() string {
	return "miniflux_hardening"
}

var removedEmbeddedElements = []string{"applet", "canvas", "embed", "math", "object", "svg"}

func (p *hardeningPlugin) Init(conv *converter.Converter) error {
	for _, tag := range removedEmbeddedElements {
		conv.Register.TagType(tag, converter.TagTypeRemove, converter.PriorityStandard)
	}
	// Runs after the base plugin's removal pass so the walk sees an already
	// pruned tree (script, style, comments, iframes, ...).
	conv.Register.PreRenderer(p.preRenderSanitize, converter.PriorityEarly+1)

	return nil
}

func (p *hardeningPlugin) preRenderSanitize(_ converter.Context, doc *html.Node) {
	var hidden []*html.Node
	walkHTMLNodes(doc, func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}
		if isHiddenElement(node) {
			hidden = append(hidden, node)

			return
		}
		stripUnsafeURLAttributes(node)
	})
	for _, node := range hidden {
		dom.RemoveNode(node)
	}
}

func walkHTMLNodes(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTMLNodes(child, visit)
	}
}

// isHiddenElement reports whether an element is concealed from ordinary
// rendering via the hidden attribute or inline display/visibility styles.
// Class-based hiding is deliberately not detected; this is a normalization
// step, not a sandbox.
func isHiddenElement(node *html.Node) bool {
	if _, ok := dom.GetAttribute(node, "hidden"); ok {
		return true
	}
	style := strings.Join(strings.Fields(strings.ToLower(dom.GetAttributeOr(node, "style", ""))), "")

	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

// stripUnsafeURLAttributes removes href/src attributes whose scheme is not
// explicitly allowed. The link or image text is preserved; only the
// attribute is dropped. Relative URLs are allowed and resolved against the
// entry URL by the converter.
func stripUnsafeURLAttributes(node *html.Node) {
	for _, name := range []string{"href", "src"} {
		value, ok := dom.GetAttribute(node, name)
		if !ok {
			continue
		}
		if !isAllowedReferenceScheme(value, name == "href") {
			removeHTMLAttribute(node, name)
		}
	}
}

func isAllowedReferenceScheme(rawURL string, allowMailto bool) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {

		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "http", "https":

		return true
	case "mailto":

		return allowMailto
	default:

		return false
	}
}

func removeHTMLAttribute(node *html.Node, key string) {
	filtered := make([]html.Attribute, 0, len(node.Attr))
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			continue
		}
		filtered = append(filtered, attribute)
	}
	node.Attr = filtered
}

// entryContentConverter is shared across conversions: the converter holds no
// per-document state and is safe for concurrent use. Output is deterministic
// for identical input, which keeps content_offset pagination stable.
var entryContentConverter = converter.NewConverter(
	converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(),
		strikethrough.NewStrikethroughPlugin(),
		newHardeningPlugin(),
	),
)

// renderEntryMarkdown converts feed HTML to Markdown. baseURL (the entry URL)
// resolves relative links to absolute ones. Errors fail closed: callers must
// never fall back to raw feed HTML.
func renderEntryMarkdown(content, baseURL string) (string, error) {
	if content == "" {
		return "", nil
	}
	options := make([]converter.ConvertOptionFunc, 0, 1)
	if baseURL != "" {
		options = append(options, converter.WithDomain(baseURL))
	}

	return entryContentConverter.ConvertString(content, options...)
}

// entryWithPresentedContent returns a copy of the entry whose content is
// presented in the configured format: normalized HTML, or Markdown converted
// from the HTML. The original entry is never mutated because clients may
// share entry pointers across paginated calls.
func (s *MinifluxServer) entryWithPresentedContent(entry *client.Entry) (*client.Entry, error) {
	if entry == nil {
		return nil, nil
	}
	if s.contentFormat == contentFormatHTML {
		return entryWithValidUTF8Content(entry), nil
	}
	markdown, err := renderEntryMarkdown(validUTF8(entry.Content), entry.URL)
	if err != nil {

		return nil, err
	}
	result := *entry
	result.Content = markdown

	return &result, nil
}

// entriesWithPresentedContent applies entryWithPresentedContent to a batch.
func (s *MinifluxServer) entriesWithPresentedContent(entries client.Entries) (client.Entries, error) {
	if s.contentFormat == contentFormatHTML {
		return entries, nil
	}
	presented := make(client.Entries, 0, len(entries))
	for _, entry := range entries {
		entry, err := s.entryWithPresentedContent(entry)
		if err != nil {

			return nil, err
		}
		presented = append(presented, entry)
	}

	return presented, nil
}
