package rss_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed/rss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustBase(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// Relative channel/item links, enclosures and text-URL elements resolve
// against the document base, including through nested relative xml:base
// declarations that stack per RFC 3986.
func TestParser_ParseWithBase_RelativeURLs(t *testing.T) {
	const feed = `
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/">
  <channel>
    <link>site/</link>
    <docs>docs/spec.html</docs>
    <image>
      <url>img/logo.png</url>
      <link>img/</link>
      <title>Logo</title>
    </image>
    <item>
      <title>First</title>
      <link>posts/1</link>
      <comments>posts/1/comments</comments>
      <enclosure url="audio/1.mp3" type="audio/mpeg" length="9"/>
      <media:content url="video/1.mp4" type="video/mp4"/>
    </item>
    <item xml:base="https://cdn.example.com/media/">
      <title>Second</title>
      <link>posts/2</link>
      <enclosure url="2.mp3" type="audio/mpeg" length="9"/>
      <guid isPermaLink="false">guid-relative/2</guid>
    </item>
  </channel>
</rss>`

	f, err := (&rss.Parser{}).ParseWithBase(
		strings.NewReader(feed),
		mustBase(t, "https://feed.example.com/feed/index.xml"),
	)
	require.NoError(t, err)
	require.NotNil(t, f)

	assert.Equal(t, "https://feed.example.com/feed/site/", f.Link)
	assert.Equal(t, "https://feed.example.com/feed/docs/spec.html", f.Docs)
	require.NotNil(t, f.Image)
	assert.Equal(t, "https://feed.example.com/feed/img/logo.png", f.Image.URL)
	assert.Equal(t, "https://feed.example.com/feed/img/", f.Image.Link)

	require.Len(t, f.Items, 2)

	first := f.Items[0]
	assert.Equal(t, "https://feed.example.com/feed/posts/1", first.Link)
	assert.Equal(t, "https://feed.example.com/feed/posts/1/comments", first.Comments)
	require.Len(t, first.Enclosures, 1)
	assert.Equal(t, "https://feed.example.com/feed/audio/1.mp3", first.Enclosures[0].URL)
	media := first.Extensions["media"]["content"][0]
	assert.Equal(t, "https://feed.example.com/feed/video/1.mp4", media.Attrs["url"])

	// The absolute xml:base on the second item scopes that item's URLs and
	// does not leak back to the first item.
	second := f.Items[1]
	assert.Equal(t, "https://cdn.example.com/media/posts/2", second.Link)
	require.Len(t, second.Enclosures, 1)
	assert.Equal(t, "https://cdn.example.com/media/2.mp3", second.Enclosures[0].URL)

	// GUIDs are identifiers and are never resolved.
	require.NotNil(t, second.GUID)
	assert.Equal(t, "guid-relative/2", second.GUID.Value)
}

// Nested relative xml:base declarations stack on top of each other and of the
// document base, and each pops back out of scope at its closing tag.
func TestParser_ParseWithBase_NestedRelativeBases(t *testing.T) {
	const feed = `
<rss version="2.0">
  <channel xml:base="archive/">
    <link>channel-link</link>
    <item xml:base="2026/">
      <link>entry-a</link>
      <enclosure url="a.mp3" type="audio/mpeg" length="1"/>
    </item>
    <item>
      <link>entry-b</link>
      <enclosure url="b.mp3" type="audio/mpeg" length="1"/>
    </item>
  </channel>
</rss>`

	f, err := (&rss.Parser{}).ParseWithBase(
		strings.NewReader(feed),
		mustBase(t, "https://example.com/feeds/feed.xml"),
	)
	require.NoError(t, err)
	require.NotNil(t, f)

	assert.Equal(t, "https://example.com/feeds/archive/channel-link", f.Link)
	require.Len(t, f.Items, 2)

	// archive/ + 2026/ stack per RFC 3986 merge rules: the relative bases
	// first combine inside the XML parser (archive/ + 2026/ -> /archive/2026/)
	// and the result is anchored to the document base.
	assert.Equal(t, "https://example.com/archive/2026/entry-a", f.Items[0].Link)
	assert.Equal(t, "https://example.com/archive/2026/a.mp3", f.Items[0].Enclosures[0].URL)

	// The item-level base is gone after its end tag: the sibling resolves
	// against the channel-level base only.
	assert.Equal(t, "https://example.com/feeds/archive/entry-b", f.Items[1].Link)
	assert.Equal(t, "https://example.com/feeds/archive/b.mp3", f.Items[1].Enclosures[0].URL)
}

// A parse without a document base must keep the historical behavior: relative
// references stay relative unless an in-document xml:base resolves them.
func TestParser_ParseWithBase_NilBasePreservesAsWritten(t *testing.T) {
	const feed = `
<rss version="2.0">
  <channel>
    <link>relative/link</link>
    <item>
      <link>posts/1</link>
      <enclosure url="audio/1.mp3" type="audio/mpeg" length="1"/>
      <guid>relative/guid</guid>
    </item>
  </channel>
</rss>`

	f, err := (&rss.Parser{}).ParseWithBase(strings.NewReader(feed), nil)
	require.NoError(t, err)
	require.NotNil(t, f)

	assert.Equal(t, "relative/link", f.Link)
	assert.Equal(t, "posts/1", f.Items[0].Link)
	assert.Equal(t, "audio/1.mp3", f.Items[0].Enclosures[0].URL)
	assert.Equal(t, "relative/guid", f.Items[0].GUID.Value)
}
