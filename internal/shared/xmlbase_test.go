package shared

import (
	"net/url"
	"testing"
)

// XmlBaseResolveUrl must not mutate the base it is given: the base is the live
// value held on the parser's xml:base stack, shared by every sibling in scope.
func TestXmlBaseResolveUrlDoesNotMutateBase(t *testing.T) {
	base, _ := url.Parse("http://example.com/a/b")
	before := base.String()

	if _, err := XmlBaseResolveUrl(base, "x"); err != nil {
		t.Fatal(err)
	}
	if after := base.String(); after != before {
		t.Errorf("base mutated: %q -> %q", before, after)
	}
}

func TestXmlBaseResolveUrlResolves(t *testing.T) {
	for _, tt := range []struct {
		base string
		ref  string
		want string
	}{
		{"http://example.com/a/b", "x", "http://example.com/a/x"},
		{"http://example.com/a/b/", "x", "http://example.com/a/b/x"},
		{"http://example.com/a/b?old=1", "?new=2", "http://example.com/a/b?new=2"},
		{"http://example.com/a/b?old=1", "#part", "http://example.com/a/b?old=1#part"},
		{"http://example.com/a/b", "", "http://example.com/a/b"},
	} {
		t.Run(tt.base+"/"+tt.ref, func(t *testing.T) {
			base, err := url.Parse(tt.base)
			if err != nil {
				t.Fatal(err)
			}
			got, err := XmlBaseResolveUrl(base, tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Errorf("resolved = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

// Every URI attribute on an element must be resolved, not just the first
// one encountered.
func TestResolveHTMLResolvesAllURIAttributes(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/")

	got, err := ResolveHTML(base, `<video poster="p.jpg" src="v.mp4"></video>`)
	if err != nil {
		t.Fatal(err)
	}
	want := `<video poster="http://example.com/dir/p.jpg" src="http://example.com/dir/v.mp4"></video>`
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

func TestResolveHTMLResolvesAcrossElements(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/")

	got, err := ResolveHTML(base, `<a href="page.html"><img src="i.png"/></a>`)
	if err != nil {
		t.Fatal(err)
	}
	want := `<a href="http://example.com/dir/page.html"><img src="http://example.com/dir/i.png"/></a>`
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

func TestResolveHTMLLeavesAbsoluteURLs(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/")

	got, err := ResolveHTML(base, `<a href="https://other.org/page.html">x</a>`)
	if err != nil {
		t.Fatal(err)
	}
	want := `<a href="https://other.org/page.html">x</a>`
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

// A malformed URI in one attribute is kept as-is; it must not error out or
// stop other attributes from resolving.
func TestResolveHTMLKeepsMalformedURIs(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/")

	got, err := ResolveHTML(base, `<video poster="http://%zz" src="v.mp4"></video>`)
	if err != nil {
		t.Fatal(err)
	}
	want := `<video poster="http://%zz" src="http://example.com/dir/v.mp4"></video>`
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

func TestResolveHTMLNilBase(t *testing.T) {
	got, err := ResolveHTML(nil, `<a href="page.html">x</a>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `<a href="page.html">x</a>` {
		t.Errorf("resolved = %q, want input unchanged", got)
	}
}

// Plain text must survive the html.Render round trip: the document wrapper
// the renderer adds has to be stripped back off.
func TestResolveHTMLPlainTextRoundTrip(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/")

	got, err := ResolveHTML(base, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("resolved = %q, want %q", got, "hello world")
	}
}

// EffectiveBase combines a document-level source URL with the xml:base in
// scope. The in-document base wins whenever it is already absolute; a still
// relative base (one declared before any absolute base was introduced) is
// anchored to the source URL.
func TestEffectiveBase(t *testing.T) {
	doc, _ := url.Parse("https://feed.example.com/feed/index.xml")

	for _, tc := range []struct {
		name    string
		inScope string
		want    string
	}{
		{"no in-scope base", "", "https://feed.example.com/feed/index.xml"},
		{"absolute in-scope base", "https://cdn.example.com/assets/", "https://cdn.example.com/assets/"},
		{"relative directory base", "entries/", "https://feed.example.com/feed/entries/"},
		{"relative root-relative base", "/shared/", "https://feed.example.com/shared/"},
		{"nested relative base", "archive/2026/", "https://feed.example.com/feed/archive/2026/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inScope *url.URL
			if tc.inScope != "" {
				inScope, _ = url.Parse(tc.inScope)
			}
			got := EffectiveBase(doc, inScope)
			if got == nil || got.String() != tc.want {
				t.Errorf("EffectiveBase(doc, %q) = %v, want %q", tc.inScope, got, tc.want)
			}
		})
	}
}

// Without a document base, EffectiveBase must return the in-scope xml:base
// unchanged so the no-source parse path keeps its historical behavior.
func TestEffectiveBaseNoDocBase(t *testing.T) {
	inScope, _ := url.Parse("entries/")
	if got := EffectiveBase(nil, inScope); got != inScope {
		t.Errorf("EffectiveBase(nil, inScope) = %v, want the in-scope base itself", got)
	}
	if got := EffectiveBase(nil, nil); got != nil {
		t.Errorf("EffectiveBase(nil, nil) = %v, want nil", got)
	}
}

// A nil in-scope base yields the document base itself.
func TestEffectiveBaseDocOnly(t *testing.T) {
	doc, _ := url.Parse("https://feed.example.com/feed.xml")
	if got := EffectiveBase(doc, nil); got != doc {
		t.Errorf("EffectiveBase(doc, nil) = %v, want doc itself", got)
	}
}

// EffectiveBase must not mutate either of its inputs: the in-scope base is
// the live value held on the parser's xml:base stack and the document base is
// shared across every element of one parse (and potentially across parses).
func TestEffectiveBaseDoesNotMutateInputs(t *testing.T) {
	doc, _ := url.Parse("https://feed.example.com/feed/index.xml")
	inScope, _ := url.Parse("entries/")
	docBefore, scopeBefore := doc.String(), inScope.String()

	_ = EffectiveBase(doc, inScope)

	if doc.String() != docBefore {
		t.Errorf("document base mutated: %q -> %q", docBefore, doc.String())
	}
	if inScope.String() != scopeBefore {
		t.Errorf("in-scope base mutated: %q -> %q", scopeBefore, inScope.String())
	}
}

// ResolveURL against a document base turns relative references absolute and
// leaves absolute references and nil bases untouched.
func TestResolveURL(t *testing.T) {
	base, _ := url.Parse("https://feed.example.com/feed/")
	for _, tc := range []struct {
		ref, want string
	}{
		{"post/1", "https://feed.example.com/feed/post/1"},
		{"/a.mp3", "https://feed.example.com/a.mp3"},
		{"https://other.test/x", "https://other.test/x"},
		{"", ""},
	} {
		if got := ResolveURL(base, tc.ref); got != tc.want {
			t.Errorf("ResolveURL(base, %q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
	if got := ResolveURL(nil, "relative/path"); got != "relative/path" {
		t.Errorf("ResolveURL(nil, ref) = %q, want the reference unchanged", got)
	}
}
