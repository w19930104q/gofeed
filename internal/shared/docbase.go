package shared

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/url"

	xpp "github.com/mmcdole/goxpp/v2"
)

// xmlNamespaceURI is the namespace name bound to the reserved xml prefix.
const xmlNamespaceURI = "http://www.w3.org/XML/1998/namespace"

// NewXMLParserWithBase is NewXMLParser plus an implicit document base URL.
// The base acts exactly like an xml:base attribute on the document element:
// in-document xml:base declarations stack on top of it (nested values resolve
// against it and sibling scopes return to it), URI attributes and element
// content URLs resolve against it, and every existing xml:base code path keeps
// working unchanged. A nil base returns an ordinary parser, so callers without
// a document base pay no extra cost.
//
// The base must be absolute; the universal parser validates this before
// calling. It is resolved per parse on a fresh token stream rather than stored
// on the Parser, so concurrent parses with different bases never share state.
func NewXMLParserWithBase(r io.Reader, base *url.URL) *xpp.Parser {
	if base == nil {
		return NewXMLParser(r)
	}

	// A wrapper token stream (xml.NewTokenDecoder) would look simpler, but a
	// Decoder built that way keeps no saved-token buffer, so DecodeElement's
	// ",innerxml" mode silently returns empty strings. Instead sniff the
	// document element with a throwaway decoder while TeeReader replays every
	// consumed byte, rewrite only the root start tag (which is entirely inside
	// the replayed prefix) so its effective xml:base equals the document base
	// plus the element's own declaration, and then run the real parser over
	// the rewritten prefix followed by the unread remainder of the stream,
	// using a single ordinary Decoder.
	var replay bytes.Buffer
	tee := io.TeeReader(r, &replay)
	root, err := sniffRootElement(tee, base)
	if err != nil {
		// Keep parsing the original stream so malformed XML surfaces its real
		// decoder error through the normal parser rather than here.
		return NewXMLParser(io.MultiReader(&replay, r))
	}

	tagStart, tagEnd, ok := locateRootTagSpan(replay.Bytes())
	if !ok {
		// The lexical scan could not bound the tag; parse the unmodified
		// stream (without the document base) rather than risk corruption.
		return NewXMLParser(io.MultiReader(&replay, r))
	}
	root.tagStart, root.tagEnd = tagStart, tagEnd

	rewritten := rewriteRootBase(replay.Bytes(), root)
	// replay holds exactly the bytes the sniff pass consumed; the remainder of
	// r starts where replay ends, so the two readers cover the stream once
	// with no overlap and no gap.
	stream := io.MultiReader(bytes.NewReader(rewritten), r)
	return NewXMLParser(stream)
}

// locateRootTagSpan finds the first markup declaration in data that is an
// element start tag (not an XML declaration, comment, processing instruction
// or doctype) and returns the half-open span of its complete start tag,
// including the closing '>' or '/>'. The sniff decoder has guaranteed that the
// entire root start tag already sits inside data.
func locateRootTagSpan(data []byte) (start, end int, ok bool) {
	i := 0
	for i < len(data) {
		if data[i] != '<' {
			i++
			continue
		}
		if i+1 >= len(data) {
			return 0, 0, false
		}
		switch data[i+1] {
		case '?', '!':
			// XML declaration / processing instruction / comment / doctype:
			// skip to its closing '>' (the '--' or '?' immediately before it
			// does not change the position of the '>').
			close := bytes.IndexByte(data[i:], '>')
			if close < 0 {
				return 0, 0, false
			}
			i += close + 1
			continue
		default:
			if !isNameByte(data[i+1]) {
				i++
				continue
			}
		}
		// Element start tag: scan to its closing '>', honoring quoted
		// attribute values so a '>' inside one does not end the scan early.
		j := i + 1
		var quote byte
		for j < len(data) {
			b := data[j]
			if quote != 0 {
				if b == quote {
					quote = 0
				}
			} else if b == '"' || b == '\'' {
				quote = b
			} else if b == '>' {
				return i, j + 1, true
			}
			j++
		}
		return 0, 0, false
	}
	return 0, 0, false
}

// rootElement describes the document element found by the sniff pass.
type rootElement struct {
	// tagStart and tagEnd bracket the complete root start tag in the
	// replayed byte prefix: tagEnd is the offset immediately after the
	// closing '>' (or '/>') so that prefix[:tagEnd] is the full start tag.
	tagStart int
	tagEnd   int
	// hasBase reports whether the root declares xml:base.
	hasBase bool
	// effectiveBase is the base in scope for the root: the document base,
	// resolved against the root's own relative xml:base when present.
	effectiveBase string
}

// sniffRootElement consumes tokens only up to and including the first
// StartElement, records the effective root base, and locates the start tag's
// byte span with a lexical scan (InputOffset points at the following token,
// not at the tag's closing '>'). An unparseable xml:base is ignored, matching
// the pull parser's rule that such a declaration inherits its parent's base.
func sniffRootElement(r io.Reader, docBase *url.URL) (rootElement, error) {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.CharsetReader = NewReaderLabel
	for {
		tok, err := d.Token()
		if err != nil {
			return rootElement{}, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		root := rootElement{effectiveBase: docBase.String()}
		for _, attr := range start.Attr {
			if attr.Name.Space == xmlNamespaceURI && attr.Name.Local == "base" {
				root.hasBase = true
				rel, perr := url.Parse(attr.Value)
				if perr == nil {
					root.effectiveBase = docBase.ResolveReference(rel).String()
				}
				break
			}
		}
		return root, nil
	}
}

// rewriteRootBase rewrites the root start tag in data so that, after the real
// parser tokenizes it, the root's in-scope xml:base equals
// root.effectiveBase. When the root already declares xml:base the existing
// attribute value is replaced in place, preserving its quote style and
// position; otherwise an attribute is inserted immediately before the start
// tag's closing '>'. Bytes outside the root start tag are never touched.
func rewriteRootBase(data []byte, root rootElement) []byte {
	tag := data[root.tagStart:root.tagEnd]

	if !root.hasBase {
		closeTag := bytes.LastIndexByte(tag, '>')
		if closeTag == -1 {
			return data
		}
		// A self-closing root gets the attribute before its '/>'.
		insertAt := closeTag
		if closeTag > 0 && tag[closeTag-1] == '/' {
			insertAt = closeTag - 1
		}
		insertAbs := root.tagStart + insertAt
		var out bytes.Buffer
		out.Grow(len(data) + len(root.effectiveBase) + 12)
		out.Write(data[:insertAbs])
		out.WriteString(` xml:base="`)
		out.WriteString(escapeAttrValue(root.effectiveBase))
		out.WriteString(`"`)
		out.Write(data[insertAbs:])
		return out.Bytes()
	}

	if loc, quote, ok := locateXMLBase(tag); ok {
		var out bytes.Buffer
		out.Grow(len(data) + len(root.effectiveBase))
		out.Write(data[:root.tagStart+loc.valueStart])
		out.WriteByte(quote)
		out.WriteString(escapeAttrValue(root.effectiveBase))
		out.WriteByte(quote)
		out.Write(data[root.tagStart+loc.valueEnd:])
		return out.Bytes()
	}
	// The tokenizer saw xml:base but the lexical scan could not locate it;
	// leave the bytes untouched rather than risk corrupting the document.
	return data
}

// attrLocation records a quoted attribute value's span within the root start
// tag: valueStart and valueEnd bracket the value together with both of its
// quote marks, so the whole literal can be replaced in one splice.
type attrLocation struct {
	valueStart int
	valueEnd   int
}

// locateXMLBase finds the value span of the xml:base attribute within the
// root start tag. Matching is lexical, case-sensitively on the reserved
// "xml:base" name, and understands single and double quoted values.
func locateXMLBase(tag []byte) (attrLocation, byte, bool) {
	const name = "xml:base"
	for i := 0; i+len(name) <= len(tag); {
		j := bytes.Index(tag[i:], []byte(name))
		if j < 0 {
			break
		}
		j += i
		// The match must be a whole attribute name: not part of a larger
		// name, and followed (modulo whitespace) by '='.
		if j > 0 && isNameByte(tag[j-1]) {
			i = j + len(name)
			continue
		}
		k := j + len(name)
		for k < len(tag) && isXMLSpace(tag[k]) {
			k++
		}
		if k >= len(tag) || tag[k] != '=' {
			i = j + len(name)
			continue
		}
		k++
		for k < len(tag) && isXMLSpace(tag[k]) {
			k++
		}
		if k >= len(tag) || (tag[k] != '"' && tag[k] != '\'') {
			i = j + len(name)
			continue
		}
		quote := tag[k]
		valueStart := k + 1
		relEnd := bytes.IndexByte(tag[valueStart:], quote)
		if relEnd < 0 {
			break
		}
		valueEnd := valueStart + relEnd
		// The span brackets the value including both quote marks, so the
		// rewriter replaces the whole quoted literal in one splice.
		return attrLocation{valueStart: k, valueEnd: valueEnd + 1}, quote, true
	}
	return attrLocation{}, 0, false
}

func isNameByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' ||
		(b >= '0' && b <= '9') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z')
}

func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// escapeAttrValue escapes the characters an XML attribute value cannot carry
// literally. Bases originate from url.Parse outputs, which are already
// percent-encoded, so in practice only defensive escaping is needed.
func escapeAttrValue(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
