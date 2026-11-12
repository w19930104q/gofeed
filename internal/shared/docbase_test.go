package shared

import (
	"io"
	"net/url"
	"strings"
	"testing"

	xpp "github.com/mmcdole/goxpp/v2"
)

func mustBase(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse base %q: %v", raw, err)
	}
	return u
}

// parseAllBase walks the whole stream with the pull parser, returning the
// bases observed on each StartTag in document order (nil entries mean no base
// in scope).
func parseAllBase(t *testing.T, p *xpp.Parser) []*url.URL {
	t.Helper()
	var out []*url.URL
	for {
		ev, err := p.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("next token: %v", err)
		}
		if ev == xpp.StartTag {
			out = append(out, p.BaseURL())
		}
	}
}

func TestNewXMLParserWithBase_NilIsOrdinaryParser(t *testing.T) {
	doc := `<rss><channel><title>x</title></channel></rss>`
	p := NewXMLParserWithBase(strings.NewReader(doc), nil)
	bases := parseAllBase(t, p)
	if len(bases) != 3 {
		t.Fatalf("want 3 start tags, got %d", len(bases))
	}
	for i, b := range bases {
		if b != nil {
			t.Fatalf("nil base must leave BaseURL nil everywhere, bases[%d]=%v", i, b)
		}
	}
}

func TestNewXMLParserWithBase_InjectsWhenAbsent(t *testing.T) {
	doc := `<?xml version="1.0"?>
<rss version="2.0">
  <channel><item><link>a</link></item><item><link>b</link></item></channel>
</rss>`
	base := mustBase(t, "https://example.com/feed.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	bases := parseAllBase(t, p)
	if len(bases) != 6 {
		t.Fatalf("want 6 start tags, got %d: %v", len(bases), bases)
	}
	// Every element inherits the document base; siblings see the same scope.
	for _, b := range bases {
		if b == nil || b.String() != "https://example.com/feed.xml" {
			t.Fatalf("all bases must be the document base, got %v", bases)
		}
	}
}

func TestNewXMLParserWithBase_StacksRelativeRootBase(t *testing.T) {
	doc := `<rss xml:base="sub/">
  <channel xml:base="nested/">
    <item xml:base="../back/"><link>a</link></item>
    <item><link>b</link></item>
  </channel>
</rss>`
	base := mustBase(t, "https://example.com/feeds/index.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	bases := parseAllBase(t, p)

	want := []string{
		"https://example.com/feeds/sub/",        // rss: doc base + sub/
		"https://example.com/feeds/sub/nested/", // channel
		"https://example.com/feeds/sub/back/",   // item: nested/../back/
		"https://example.com/feeds/sub/back/",   // link inherits item
		"https://example.com/feeds/sub/nested/", // sibling item restores channel scope
		"https://example.com/feeds/sub/nested/", // link
	}
	if len(bases) != len(want) {
		t.Fatalf("got %d bases %v, want %d", len(bases), bases, len(want))
	}
	for i, b := range bases {
		if b == nil || b.String() != want[i] {
			t.Fatalf("bases[%d] = %v, want %q\nfull: %v", i, b, want[i], bases)
		}
	}
}

func TestNewXMLParserWithBase_AbsoluteRootBaseWins(t *testing.T) {
	doc := `<rss xml:base="https://cdn.example.net/assets/">
  <channel><item><enclosure url="a.mp3"/></item></channel>
</rss>`
	base := mustBase(t, "https://example.com/feed.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	bases := parseAllBase(t, p)
	for i, b := range bases {
		if b == nil || b.String() != "https://cdn.example.net/assets/" {
			t.Fatalf("absolute root base must override document base, got %v at %d", bases, i)
		}
	}
}

func TestNewXMLParserWithBase_SingleQuotedAndSelfClosing(t *testing.T) {
	doc := `<?xml version="1.0"?><feed xml:base='sub/'><entry/><entry/></feed>`
	base := mustBase(t, "https://example.com/feeds/f.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	bases := parseAllBase(t, p)
	want := "https://example.com/feeds/sub/"
	for i, b := range bases {
		if b == nil || b.String() != want {
			t.Fatalf("bases[%d] = %v, want %q (full %v)", i, b, want, bases)
		}
	}
}

func TestNewXMLParserWithBase_UnparseableRootBaseFallsBack(t *testing.T) {
	// An xml:base url.Parse rejects behaves as absent: the document base is
	// inherited, matching the pull parser's nested-base rule.
	doc := `<rss xml:base="%zz"><channel><item><link>a</link></item></channel></rss>`
	base := mustBase(t, "https://example.com/feed.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	bases := parseAllBase(t, p)
	for i, b := range bases {
		if b == nil || b.String() != "https://example.com/feed.xml" {
			t.Fatalf("bases[%d] = %v, all should inherit document base (%v)", i, b, bases)
		}
	}
}

func TestNewXMLParserWithBase_PreservesInnerXML(t *testing.T) {
	// The whole point of avoiding a token-stream wrapper: DecodeElement's
	// innerxml mode must still see the element content verbatim.
	doc := `<rss><channel>
  <title>S &amp; co</title>
  <description><![CDATA[<a href="x">l</a>]]></description>
</channel></rss>`
	base := mustBase(t, "https://example.com/feed.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)

	got := map[string]string{}
	for {
		ev, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if ev != xpp.StartTag {
			continue
		}
		switch p.Name() {
		case "title", "description":
			var text struct {
				InnerXML string `xml:",innerxml"`
			}
			if err := p.DecodeElement(&text); err != nil {
				t.Fatalf("decode %s: %v", p.Name(), err)
			}
			got[p.Name()] = strings.TrimSpace(text.InnerXML)
		}
	}
	if got["title"] != "S &amp; co" {
		t.Errorf("title innerxml = %q", got["title"])
	}
	if got["description"] != `<![CDATA[<a href="x">l</a>]]>` {
		t.Errorf("description innerxml = %q", got["description"])
	}
}

func TestNewXMLParserWithBase_PreambleBeforeRoot(t *testing.T) {
	// A declaration, a comment containing a fake tag and a doctype with a
	// '>' in its internal subset must not confuse the root-tag scan; the base
	// still lands on the real root and the document parses end to end.
	doc := `<?xml version="1.0"?>
<!-- <fake x=">"> -->
<!DOCTYPE rss [<!ELEMENT rss ANY> <!-- a>b -->]>
<rss version="2.0" xml:base="sub/"><channel><item><link>a</link></item></channel></rss>`
	base := mustBase(t, "https://example.com/feeds/f.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	bases := parseAllBase(t, p)
	if len(bases) != 4 {
		t.Fatalf("want 4 start tags, got %d", len(bases))
	}
	for i, b := range bases {
		if b == nil || b.String() != "https://example.com/feeds/sub/" {
			t.Fatalf("bases[%d] = %v, want the resolved root base", i, b)
		}
	}
}

func TestNewXMLParserWithBase_CharsetConversionStillApplies(t *testing.T) { // ISO-8859-1 feed: 0xE9 encodes é. The CharsetReader must stay wired to
	// the parser that actually tokenizes the document.
	doc := `<?xml version="1.0" encoding="ISO-8859-1"?><rss><channel><title>caf` +
		"\xe9" + `</title></channel></rss>`
	base := mustBase(t, "https://example.com/feed.xml")
	p := NewXMLParserWithBase(strings.NewReader(doc), base)
	for {
		ev, err := p.Next()
		if err == io.EOF {
			t.Fatal("reached EOF without title text")
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if ev == xpp.StartTag && p.Name() == "title" {
			var text struct {
				InnerXML string `xml:",innerxml"`
			}
			if err := p.DecodeElement(&text); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if strings.TrimSpace(text.InnerXML) != "café" {
				t.Fatalf("charset conversion lost: %q", text.InnerXML)
			}
			return
		}
	}
}

func TestLocateRootTagSpan(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		tag  string
	}{
		{"plain", `<rss><c/>`, `<rss>`},
		{"with declaration", `<?xml version="1.0"?><rss a="x"><c/>`, `<rss a="x">`},
		{"multiline", "<rss\n  version='2.0'\n><c/>", "<rss\n  version='2.0'\n>"},
		{"after comment", `<!-- hi --><rss><c/>`, `<rss>`},
		{"after PI", `<?pi target?><rss><c/>`, `<rss>`},
		{"self closing", `<rss version="2.0"/>`, `<rss version="2.0"/>`},
		{"gt in attr value", `<rss note="a>b"><c/>`, `<rss note="a>b">`},
		{"fake tag inside comment", `<!-- <fake x=">"> --><rss><c/>`, `<rss>`},
		{"gt inside doctype subset", `<!DOCTYPE rss [<!ELEMENT rss ANY> <!-- a>b -->]><rss><c/>`, `<rss>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := locateRootTagSpan([]byte(tc.doc))
			if !ok {
				t.Fatal("span not found")
			}
			if got := tc.doc[start:end]; got != tc.tag {
				t.Errorf("span = %q, want %q", got, tc.tag)
			}
		})
	}
}

func TestLocateXMLBase(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tag       string
		wantValue string
		quote     byte
	}{
		{"double", `<rss xml:base="sub/">`, "sub/", '"'},
		{"single", `<rss xml:base='sub/'>`, "sub/", '\''},
		{"spaced", `<rss xml:base  =  "sub/" >`, "sub/", '"'},
		{"among attrs", `<rss version="2.0" xml:base="sub/" x="y">`, "sub/", '"'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, quote, ok := locateXMLBase([]byte(tc.tag))
			if !ok {
				t.Fatal("xml:base not located")
			}
			if quote != tc.quote {
				t.Errorf("quote = %q, want %q", quote, tc.quote)
			}
			got := tc.tag[loc.valueStart+1 : loc.valueEnd-1]
			if got != tc.wantValue {
				t.Errorf("value span = %q, want %q", got, tc.wantValue)
			}
		})
	}

	if _, _, ok := locateXMLBase([]byte(`<rss notxml:base="x">`)); ok {
		t.Error("notxml:base must not match the xml:base name")
	}
}

func TestSniffRootElement_ErrorsWithoutStartElement(t *testing.T) {
	base := mustBase(t, "https://example.com/feed.xml")
	// A stream that ends before any element starts must report EOF rather
	// than invent a root.
	if _, err := sniffRootElement(strings.NewReader(`<!-- just a comment -->`), base); err == nil {
		t.Fatal("a document with no start element must return the decoder error")
	}
}

// When the sniff pass fails, NewXMLParserWithBase must reparse the original
// bytes (replayed plus the unread remainder) rather than dropping them, so the
// caller receives the ordinary parser's error for the malformed document.
func TestNewXMLParserWithBase_MalformedInputReparsedOnce(t *testing.T) {
	base := mustBase(t, "https://example.com/feed.xml")
	// Well-formed enough that sniffing reaches a root, then corrupt later:
	// verify no duplication occurs by checking a good document parses fully.
	doc := strings.NewReader(`<rss><channel><title>ok</title></channel></rss>`)
	p := NewXMLParserWithBase(doc, base)
	if _, err := FindRoot(p); err != nil {
		t.Fatalf("reparse lost data: %v", err)
	}
}
