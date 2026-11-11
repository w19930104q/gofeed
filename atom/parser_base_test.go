package atom_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed/atom"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustAtomBase(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// Feed- and entry-level links, content src and URI elements resolve against
// the document base; the atom:id is an identifier and stays as written.
func TestParser_ParseWithBase_RelativeURLs(t *testing.T) {
	const feed = `
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:uuid:feed-id</id>
  <link rel="self" href="feed.xml"/>
  <link href="site/"/>
  <icon>favicon.ico</icon>
  <logo>img/logo.png</logo>
  <author><name>A</name><uri>authors/a</uri></author>
  <entry>
    <id>relative/entry-id</id>
    <link rel="alternate" href="posts/1"/>
    <link rel="enclosure" href="audio/1.mp3" type="audio/mpeg" length="9"/>
    <content type="html" src="content/1.html">&lt;a href="details/1"&gt;more&lt;/a&gt;&lt;img src="img/1.png"/&gt;</content>
  </entry>
</feed>`

	f, err := (&atom.Parser{}).ParseWithBase(
		strings.NewReader(feed),
		mustAtomBase(t, "https://feed.example.com/feed/index.xml"),
	)
	require.NoError(t, err)
	require.NotNil(t, f)

	// The feed id is never resolved against the document base.
	assert.Equal(t, "urn:uuid:feed-id", f.ID)
	assert.Equal(t, "https://feed.example.com/feed/feed.xml", feedSelfHref(f, "self"))
	assert.Equal(t, "https://feed.example.com/feed/site/", feedSelfHref(f, "alternate"))
	assert.Equal(t, "https://feed.example.com/feed/favicon.ico", f.Icon)
	assert.Equal(t, "https://feed.example.com/feed/img/logo.png", f.Logo)
	require.Len(t, f.Authors, 1)
	assert.Equal(t, "https://feed.example.com/feed/authors/a", f.Authors[0].URI)

	require.Len(t, f.Entries, 1)
	entry := f.Entries[0]

	// Entry id keeps its document value even though it looks relative.
	assert.Equal(t, "relative/entry-id", entry.ID)

	var alternate, enclosure *atom.Link
	for _, l := range entry.Links {
		switch l.Rel {
		case "alternate":
			alternate = l
		case "enclosure":
			enclosure = l
		}
	}
	require.NotNil(t, alternate)
	assert.Equal(t, "https://feed.example.com/feed/posts/1", alternate.Href)
	require.NotNil(t, enclosure)
	assert.Equal(t, "https://feed.example.com/feed/audio/1.mp3", enclosure.Href)

	require.NotNil(t, entry.Content)
	assert.Equal(t, "https://feed.example.com/feed/content/1.html", entry.Content.Src)
	// href and src inside the HTML content resolve against the same base.
	assert.Contains(t, entry.Content.Value, `href="https://feed.example.com/feed/details/1"`)
	assert.Contains(t, entry.Content.Value, `src="https://feed.example.com/feed/img/1.png"`)
}

// Nested relative xml:base declarations stack and stay scoped: a base on one
// entry must not change how a sibling entry resolves.
func TestParser_ParseWithBase_NestedBasesAndSiblingScope(t *testing.T) {
	const feed = `
<feed xmlns="http://www.w3.org/2005/Atom" xml:base="archive/">
  <entry xml:base="2026/">
    <id>id-a</id>
    <link href="entry-a"/>
    <summary type="html">&lt;a href="more"&gt;x&lt;/a&gt;</summary>
  </entry>
  <entry>
    <id>id-b</id>
    <link href="entry-b"/>
  </entry>
</feed>`

	f, err := (&atom.Parser{}).ParseWithBase(
		strings.NewReader(feed),
		mustAtomBase(t, "https://example.com/feeds/feed.xml"),
	)
	require.NoError(t, err)
	require.NotNil(t, f)
	require.Len(t, f.Entries, 2)

	// archive/ + 2026/ combine per RFC 3986 before anchoring to the doc base.
	assert.Equal(t, "https://example.com/archive/2026/entry-a", f.Entries[0].Links[0].Href)
	assert.Contains(t, f.Entries[0].Summary, `href="https://example.com/archive/2026/more"`)

	// The sibling entry never sees the first entry's xml:base.
	assert.Equal(t, "https://example.com/feeds/archive/entry-b", f.Entries[1].Links[0].Href)

	// ids are untouched regardless of the bases in scope.
	assert.Equal(t, "id-a", f.Entries[0].ID)
	assert.Equal(t, "id-b", f.Entries[1].ID)
}

// An in-document absolute xml:base still wins over the document base, and an
// id declared inside that scope keeps resolving the historical xml:base way.
func TestParser_ParseWithBase_AbsoluteXMLBaseAndID(t *testing.T) {
	const feed = `
<feed xmlns="http://www.w3.org/2005/Atom" xml:base="https://cdn.example.com/assets/">
  <link href="feed.xml"/>
  <entry>
    <id>relative/id</id>
    <link href="entry"/>
  </entry>
</feed>`

	f, err := (&atom.Parser{}).ParseWithBase(
		strings.NewReader(feed),
		mustAtomBase(t, "https://feed.example.com/feed.xml"),
	)
	require.NoError(t, err)
	require.NotNil(t, f)

	assert.Equal(t, "https://cdn.example.com/assets/feed.xml", f.Links[0].Href)
	require.Len(t, f.Entries, 1)
	assert.Equal(t, "https://cdn.example.com/assets/entry", f.Entries[0].Links[0].Href)
	// atom:id is an identifier: it stays verbatim even under an absolute
	// in-document xml:base.
	assert.Equal(t, "relative/id", f.Entries[0].ID)
}

// A nil document base reproduces the historical parse: relative values stay
// relative without an in-document xml:base.
func TestParser_ParseWithBase_NilBasePreservesAsWritten(t *testing.T) {
	const feed = `
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>feed-id</id>
  <link href="feed.xml"/>
  <entry>
    <id>entry-id</id>
    <link href="posts/1"/>
    <content type="html">&lt;a href="details"&gt;x&lt;/a&gt;</content>
  </entry>
</feed>`

	f, err := (&atom.Parser{}).ParseWithBase(strings.NewReader(feed), nil)
	require.NoError(t, err)
	require.NotNil(t, f)

	assert.Equal(t, "feed.xml", f.Links[0].Href)
	require.Len(t, f.Entries, 1)
	assert.Equal(t, "entry-id", f.Entries[0].ID)
	assert.Equal(t, "posts/1", f.Entries[0].Links[0].Href)
	assert.Contains(t, f.Entries[0].Content.Value, `href="details"`)
}

func feedSelfHref(f *atom.Feed, rel string) string {
	for _, l := range f.Links {
		if l.Rel == rel {
			return l.Href
		}
	}
	return ""
}
