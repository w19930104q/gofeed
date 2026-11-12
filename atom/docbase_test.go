package atom_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed/atom"
	"github.com/mmcdole/gofeed/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseAtomWithBase(t *testing.T, doc, base string) *atom.Feed {
	t.Helper()
	var u *url.URL
	if base != "" {
		var err error
		u, err = url.Parse(base)
		require.NoError(t, err)
	}
	f, err := (&atom.Parser{}).ParseWithBase(strings.NewReader(doc), u)
	require.NoError(t, err)
	return f
}

// All Atom URI-bearing feed and entry fields resolve against the document
// base and nested xml:base declarations.
func TestParser_ParseWithBase_ResolvesAllURLFields(t *testing.T) {
	doc := `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:uuid:feed</id>
  <link href="alt.xml" rel="alternate"/>
  <link href="self.xml" rel="self"/>
  <icon>icon.png</icon>
  <logo>logo.png</logo>
  <generator uri="gen.html">g</generator>
  <author><name>Jane</name><uri>people/jane</uri></author>
  <contributor><name>Bob</name><uri>people/bob</uri></contributor>
  <entry xml:base="entries/">
    <id>urn:uuid:1</id>
    <link href="one.html"/>
    <link href="a.mp4" rel="enclosure" type="video/mp4" length="9"/>
    <author><name>Jo</name><uri>people/jo</uri></author>
  </entry>
  <entry>
    <link href="two.html"/>
  </entry>
</feed>`
	f := parseAtomWithBase(t, doc, "https://example.com/feeds/feed.xml")
	const feedBase = "https://example.com/feeds/"
	const entryBase = "https://example.com/feeds/entries/"

	require.Len(t, f.Links, 2)
	assert.Equal(t, feedBase+"alt.xml", f.Links[0].Href)
	assert.Equal(t, feedBase+"self.xml", f.Links[1].Href)
	assert.Equal(t, feedBase+"icon.png", f.Icon)
	assert.Equal(t, feedBase+"logo.png", f.Logo)
	require.NotNil(t, f.Generator)
	assert.Equal(t, feedBase+"gen.html", f.Generator.URI)
	require.Len(t, f.Authors, 1)
	assert.Equal(t, feedBase+"people/jane", f.Authors[0].URI)
	require.Len(t, f.Contributors, 1)
	assert.Equal(t, feedBase+"people/bob", f.Contributors[0].URI)

	require.Len(t, f.Entries, 2)
	e0 := f.Entries[0]
	// IDs keep following the element's xml:base semantics; urn: identifiers
	// are absolute URIs and therefore unchanged.
	assert.Equal(t, "urn:uuid:1", e0.ID)
	require.Len(t, e0.Links, 2)
	assert.Equal(t, entryBase+"one.html", e0.Links[0].Href)
	assert.Equal(t, entryBase+"a.mp4", e0.Links[1].Href)
	require.Len(t, e0.Authors, 1)
	assert.Equal(t, entryBase+"people/jo", e0.Authors[0].URI)

	// The sibling entry returns to the feed-level scope.
	require.Len(t, f.Entries[1].Links, 1)
	assert.Equal(t, feedBase+"two.html", f.Entries[1].Links[0].Href)
}

// XHTML and HTML content resolves its URI attributes against the in-scope
// base, including the nested entry base.
func TestParser_ParseWithBase_ResolvesContentHTML(t *testing.T) {
	doc := `<feed xmlns="http://www.w3.org/2005/Atom">
  <entry xml:base="posts/1/">
    <content type="xhtml">
      <div xmlns="http://www.w3.org/1999/xhtml"><a href="detail">d</a><img src="pic.png"/></div>
    </content>
    <summary type="html">&lt;a href="sum"&gt;s&lt;/a&gt;</summary>
  </entry>
</feed>`
	f := parseAtomWithBase(t, doc, "https://example.com/feeds/")
	require.Len(t, f.Entries, 1)
	e := f.Entries[0]
	require.NotNil(t, e.Content)
	assert.Contains(t, e.Content.Value, `href="https://example.com/feeds/posts/1/detail"`)
	assert.Contains(t, e.Content.Value, `src="https://example.com/feeds/posts/1/pic.png"`)
	assert.Contains(t, e.Summary, `href="https://example.com/feeds/posts/1/sum"`)
}

// Atom 0.3 author homepage elements are URI elements too.
func TestParser_ParseWithBase_ResolvesHomepage(t *testing.T) {
	doc := `<feed xmlns="http://purl.org/atom/ns#" version="0.3">
	  <author><name>Jane</name><homepage>~jane</homepage></author>
	  <entry><author><name>Jo</name><homepage>~jo</homepage></author></entry>
	</feed>`
	f := parseAtomWithBase(t, doc, "https://example.com/feeds/feed.xml")
	const base = "https://example.com/feeds/"
	require.NotEmpty(t, f.Authors)
	assert.Equal(t, base+"~jane", f.Authors[0].URI)
	require.Len(t, f.Entries, 1)
	require.NotEmpty(t, f.Entries[0].Authors)
	assert.Equal(t, base+"~jo", f.Entries[0].Authors[0].URI)
}

// A content element's src attribute resolves like any other URI attribute.
func TestParser_ParseWithBase_ResolvesContentSrc(t *testing.T) {
	doc := `<feed xmlns="http://www.w3.org/2005/Atom">
	  <entry><content type="text/html" src="remote/fragment.html"/></entry>
	</feed>`
	f := parseAtomWithBase(t, doc, "https://example.com/feeds/feed.xml")
	require.Len(t, f.Entries, 1)
	require.NotNil(t, f.Entries[0].Content)
	assert.Equal(t, "https://example.com/feeds/remote/fragment.html",
		f.Entries[0].Content.Src)
}

// atom:id is excluded from relative-IRI resolution (RFC 4287 section 3.1.1),
// so feed, entry and source IDs stay exactly as authored even when a document
// base or xml:base would otherwise resolve them.
func TestParser_ParseWithBase_KeepsIDsAsAuthored(t *testing.T) {
	doc := `<feed xmlns="http://www.w3.org/2005/Atom" xml:base="rel/">
	  <id>feed/relative-id</id>
	  <entry>
	    <id>entry/relative-id</id>
	    <source><id>source/relative-id</id></source>
	  </entry>
	</feed>`
	f := parseAtomWithBase(t, doc, "https://example.com/feeds/f.xml")
	assert.Equal(t, "feed/relative-id", f.ID)
	require.Len(t, f.Entries, 1)
	assert.Equal(t, "entry/relative-id", f.Entries[0].ID)
	require.NotNil(t, f.Entries[0].Source)
	assert.Equal(t, "source/relative-id", f.Entries[0].Source.ID)
}

func TestParser_ParseWithBase_NilEqualsParse(t *testing.T) {
	data := testutil.ReadFile(t, "../testdata/parser/atom/entry_content_xml_base_inherit.xml")
	a, err := (&atom.Parser{}).Parse(strings.NewReader(string(data)))
	require.NoError(t, err)
	b, err := (&atom.Parser{}).ParseWithBase(strings.NewReader(string(data)), nil)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestParser_ParseWithBase_ConcurrentIsolation(t *testing.T) {
	doc := `<feed xmlns="http://www.w3.org/2005/Atom">
	  <entry><link href="post"/></entry></feed>`
	p := &atom.Parser{}
	done := make(chan error, 2)
	for _, base := range []string{"https://a.example/feeds/", "https://b.example/feeds/"} {
		base := base
		go func() {
			u, _ := url.Parse(base)
			for i := 0; i < 50; i++ {
				f, err := p.ParseWithBase(strings.NewReader(doc), u)
				if err != nil {
					done <- err
					return
				}
				if got := f.Entries[0].Links[0].Href; got != base+"post" {
					done <- assert.AnError
					return
				}
			}
			done <- nil
		}()
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, <-done)
	}
}
