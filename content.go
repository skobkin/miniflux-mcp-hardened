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
// whose scheme could trigger agent- or browser-side execution. Markup is
// removed or unwrapped, never article text; censoring visible text is out
// of scope.
type hardeningPlugin struct{}

func newHardeningPlugin() converter.Plugin {
	return &hardeningPlugin{}
}

func (p *hardeningPlugin) Name() string {
	return "miniflux_hardening"
}

var removedEmbeddedElements = []string{"applet", "canvas", "embed", "math", "object", "svg", "template"}

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
	var strippedLinks []*html.Node
	var strippedImages []*html.Node
	walkHTMLNodes(doc, func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}
		if isHiddenElement(node) || isClosedDialog(node) {
			hidden = append(hidden, node)

			return
		}
		switch stripUnsafeURLAttributes(node) {
		case urlAttributeLinkStripped:
			strippedLinks = append(strippedLinks, node)
		case urlAttributeImageStripped:
			strippedImages = append(strippedImages, node)
		}
	})
	// Structural changes run after the walk and after removals so iteration
	// never mutates the tree mid-traversal.
	for _, node := range hidden {
		dom.RemoveNode(node)
	}
	for _, node := range strippedLinks {
		unwrapNode(node)
	}
	for _, node := range strippedImages {
		replaceImageWithAltText(node)
	}
}

func walkHTMLNodes(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTMLNodes(child, visit)
	}
}

// isClosedDialog reports whether a dialog element lacks the open attribute;
// browsers do not render the content of such dialogs.
func isClosedDialog(node *html.Node) bool {
	if dom.NodeName(node) != "dialog" {
		return false
	}
	_, open := dom.GetAttribute(node, "open")

	return !open
}

// isHiddenElement reports whether an element is concealed from ordinary
// rendering via the hidden attribute or inline display/visibility styles
// (including CSS-comment obfuscation and visibility:collapse). Class-based
// hiding, opacity, positioning, font-size tricks, and CSS backslash escapes
// are deliberately not detected; this is a normalization step, not a
// sandbox.
func isHiddenElement(node *html.Node) bool {
	if _, ok := dom.GetAttribute(node, "hidden"); ok {
		return true
	}
	style := strings.Join(strings.Fields(stripCSSComments(strings.ToLower(dom.GetAttributeOr(node, "style", "")))), "")

	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") || strings.Contains(style, "visibility:collapse")
}

// stripCSSComments removes /* ... */ comments so encodings such as
// display:/*x*/none cannot evade the concealment check. An unclosed
// comment truncates the remaining value, which errs toward removal.
func stripCSSComments(value string) string {
	for {
		start := strings.Index(value, "/*")
		if start < 0 {
			return value
		}
		end := strings.Index(value[start+2:], "*/")
		if end < 0 {
			return value[:start]
		}
		value = value[:start] + value[start+2+end+2:]
	}
}

type urlAttributeAction uint8

const (
	urlAttributeUnchanged urlAttributeAction = iota
	urlAttributeLinkStripped
	urlAttributeImageStripped
)

// stripUnsafeURLAttributes removes href/src attributes whose scheme is not
// explicitly allowed. Stripped links are reported for unwrapping so the
// link text remains as plain text instead of being rendered with a
// fabricated destination; stripped images are reported for replacement by
// their alt text. Relative URLs are allowed and resolved against the entry
// URL by the converter.
func stripUnsafeURLAttributes(node *html.Node) urlAttributeAction {
	action := urlAttributeUnchanged
	for _, name := range []string{"href", "src"} {
		value, ok := dom.GetAttribute(node, name)
		if !ok {
			continue
		}
		if isAllowedReferenceScheme(value, name == "href") {
			continue
		}
		removeHTMLAttribute(node, name)
		switch {
		case dom.NodeName(node) == "a":
			action = urlAttributeLinkStripped
		case dom.NodeName(node) == "img":
			action = urlAttributeImageStripped
		}
	}

	return action
}

// unwrapNode moves a node's children into its parent and removes the node.
func unwrapNode(node *html.Node) {
	parent := node.Parent
	if parent == nil {
		dom.RemoveNode(node)

		return
	}
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		dom.RemoveNode(child)
		parent.InsertBefore(child, node)
		child = next
	}
	dom.RemoveNode(node)
}

// replaceImageWithAltText removes an image whose source was stripped,
// keeping its alt text as ordinary text so the caption is not lost.
func replaceImageWithAltText(node *html.Node) {
	parent := node.Parent
	alt := strings.TrimSpace(dom.GetAttributeOr(node, "alt", ""))
	if parent == nil || alt == "" {
		dom.RemoveNode(node)

		return
	}
	text := &html.Node{Type: html.TextNode, Data: alt}
	parent.InsertBefore(text, node)
	dom.RemoveNode(node)
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

// entryWithPresentedContent returns an entry whose content is presented in
// the configured format: normalized HTML, or Markdown converted from the
// HTML. The input entry is never mutated because clients may share entry
// pointers across paginated calls; in HTML mode the input may be returned
// unchanged.
func entryWithPresentedContent(entry *client.Entry, format contentFormat) (*client.Entry, error) {
	if entry == nil {
		return nil, nil
	}
	if format == contentFormatHTML {
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

// contentWithheldNotice replaces article content when conversion fails. It
// keeps a single unprocessable entry from failing an entire digest on every
// call while the entry sits unread in the queue; raw feed HTML is never
// emitted as a fallback.
const contentWithheldNotice = "[article content withheld: processing failed]"

// presentEntriesWithFallback is presentEntries with per-entry degradation:
// an entry whose conversion fails is presented with withheld content and
// intact metadata instead of aborting the batch.
func presentEntriesWithFallback(entries client.Entries, format contentFormat) client.Entries {
	presented := make(client.Entries, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		rendered, err := entryWithPresentedContent(entry, format)
		if err != nil {
			withheld := *entry
			withheld.Content = contentWithheldNotice
			rendered = &withheld
		}
		presented = append(presented, rendered)
	}

	return presented
}
