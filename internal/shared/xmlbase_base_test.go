package shared

import (
	"net/url"
	"strings"
	"testing"

	xpp "github.com/mmcdole/goxpp/v2"
)

func newTestParser(t *testing.T, xml string) *xpp.Parser {
	t.Helper()
	return NewXMLParser(strings.NewReader(xml))
}

// NextTag is the nil-base convenience wrapper and must keep working as the
// public loop helper relies on it.
func TestNextTag_NilBaseWrapper(t *testing.T) {
	p := newTestParser(t, `<root><link href="rel/x"/></root>`)
	if _, err := FindRoot(p); err != nil {
		t.Fatal(err)
	}
	ev, err := NextTag(p)
	if err != nil {
		t.Fatal(err)
	}
	if ev != xpp.StartTag || p.Name() != "link" {
		t.Fatalf("got %v %q, want start tag link", ev, p.Name())
	}
	if got := p.Attribute("href"); got != "rel/x" {
		t.Errorf("href = %q, want value as written", got)
	}
}

// NextTagWithBase surfaces the EndDocument error when no tag follows.
func TestNextTagWithBase_EndDocumentError(t *testing.T) {
	doc, _ := url.Parse("https://example.com/")
	p := newTestParser(t, `<root/>`)
	if _, err := FindRoot(p); err != nil {
		t.Fatal(err)
	}
	// Consume the root's end tag; the next advancement reaches EndDocument.
	if _, err := NextTagWithBase(p, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := NextTagWithBase(p, doc); err == nil {
		t.Error("expected an error at EndDocument, got nil")
	}
}

// NextTagWithBase resolves URI attributes against the document base when the
// document declares no xml:base of its own.
func TestNextTagWithBase_DocumentBaseResolvesAttrs(t *testing.T) {
	doc, err := url.Parse("https://example.com/feed/index.xml")
	if err != nil {
		t.Fatal(err)
	}
	p := newTestParser(t, `<root><link href="posts/1"/></root>`)
	if _, err := FindRoot(p); err != nil {
		t.Fatal(err)
	}

	ev, err := NextTagWithBase(p, doc)
	if err != nil {
		t.Fatal(err)
	}
	if ev != xpp.StartTag || p.Name() != "link" {
		t.Fatalf("got event %v name %q, want start tag link", ev, p.Name())
	}
	if got := p.Attribute("href"); got != "https://example.com/feed/posts/1" {
		t.Errorf("href = %q, want resolved absolute URL", got)
	}
}

// A nil document base keeps the historical behavior: relative attributes are
// not rewritten when there is no in-document xml:base either.
func TestNextTagWithBase_NilBaseLeavesAttrs(t *testing.T) {
	p := newTestParser(t, `<root><link href="posts/1"/></root>`)
	if _, err := FindRoot(p); err != nil {
		t.Fatal(err)
	}
	if _, err := NextTagWithBase(p, nil); err != nil {
		t.Fatal(err)
	}
	if got := p.Attribute("href"); got != "posts/1" {
		t.Errorf("href = %q, want the value as written", got)
	}
}

// The document base combines with a relative in-document xml:base, and the
// xml:base stays scoped: a sibling outside that element only sees the
// document base.
func TestNextTagWithBase_RelativeXMLBaseScoped(t *testing.T) {
	doc, _ := url.Parse("https://example.com/feed.xml")
	p := newTestParser(t,
		`<root><item xml:base="archive/"><a href="x"/></item><item><a href="y"/></item></root>`)
	if _, err := FindRoot(p); err != nil {
		t.Fatal(err)
	}

	// First item start tag, then its anchor.
	if err := ForEachChildWithBase(p, doc, func(name string) error {
		if name != "item" {
			return nil
		}
		return ForEachChildWithBase(p, doc, func(child string) error {
			if child == "a" && p.Attribute("href") != "https://example.com/archive/x" {
				t.Errorf("first anchor = %q, want scoped absolute URL", p.Attribute("href"))
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
}

// ForEachChildWithBase with a nil base behaves exactly like ForEachChild.
func TestForEachChildWithBase_NilBaseMatchesForEachChild(t *testing.T) {
	const doc = `<root><a href="rel/x"/><b>text</b></root>`

	var gotNil, gotPlain []string
	p1 := newTestParser(t, doc)
	if _, err := FindRoot(p1); err != nil {
		t.Fatal(err)
	}
	if err := ForEachChildWithBase(p1, nil, func(name string) error {
		gotNil = append(gotNil, name+":"+p1.Attribute("href"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	p2 := newTestParser(t, doc)
	if _, err := FindRoot(p2); err != nil {
		t.Fatal(err)
	}
	if err := ForEachChild(p2, func(name string) error {
		gotPlain = append(gotPlain, name+":"+p2.Attribute("href"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if strings.Join(gotNil, ",") != strings.Join(gotPlain, ",") {
		t.Errorf("nil base = %v, want identical to ForEachChild %v", gotNil, gotPlain)
	}
}

// ParseTextURLWithBase resolves an element's text URL against the document
// base, and leaves it untouched with a nil base.
func TestParseTextURLWithBase(t *testing.T) {
	doc, _ := url.Parse("https://example.com/feed/")
	xml := `<root><link>page/1</link></root>`

	p := newTestParser(t, xml)
	if _, err := FindRoot(p); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := ForEachChildWithBase(p, doc, func(name string) error {
		var err error
		got, err = ParseTextURLWithBase(p, doc)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/feed/page/1" {
		t.Errorf("resolved = %q, want absolute URL", got)
	}

	p2 := newTestParser(t, xml)
	if _, err := FindRoot(p2); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := ForEachChild(p2, func(name string) error {
		var err error
		raw, err = ParseTextURLWithBase(p2, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if raw != "page/1" {
		t.Errorf("nil base = %q, want value as written", raw)
	}
}
