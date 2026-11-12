package rss_test

import (
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed/internal/testutil"
	"github.com/mmcdole/gofeed/rss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseRSSWithBase(t *testing.T, doc, base string) *rss.Feed {
	t.Helper()
	var u *url.URL
	if base != "" {
		var err error
		u, err = url.Parse(base)
		require.NoError(t, err)
	}
	f, err := (&rss.Parser{}).ParseWithBase(strings.NewReader(doc), u)
	require.NoError(t, err)
	return f
}

// ParseWithBase resolves every URL-bearing RSS field against the document
// base, stacking with in-document xml:base.
func TestParser_ParseWithBase_ResolvesAllURLFields(t *testing.T) {
	doc := `<?xml version="1.0"?>
<rss version="2.0" xml:base="sub/">
  <channel>
    <title>Site</title>
    <link>index.html</link>
    <docs>help.html</docs>
    <image><url>logo.png</url><link>imgpage.html</link><title>logo</title></image>
    <textinput><title>s</title><description>d</description><name>n</name><link>search.html</link></textinput>
    <item>
      <title>Post</title>
      <link>post/1</link>
      <comments>post/1/comments</comments>
      <source url="src.xml">source</source>
      <enclosure url="media/a.mp3" length="42" type="audio/mpeg"/>
      <guid isPermaLink="false">tag:example:1</guid>
    </item>
  </channel>
</rss>`
	f := parseRSSWithBase(t, doc, "https://example.com/feeds/feed.xml")
	const root = "https://example.com/feeds/sub/"

	assert.Equal(t, root+"index.html", f.Link)
	assert.Equal(t, root+"help.html", f.Docs)
	require.NotNil(t, f.Image)
	assert.Equal(t, root+"logo.png", f.Image.URL)
	assert.Equal(t, root+"imgpage.html", f.Image.Link)
	require.NotNil(t, f.TextInput)
	assert.Equal(t, root+"search.html", f.TextInput.Link)

	require.Len(t, f.Items, 1)
	item := f.Items[0]
	assert.Equal(t, root+"post/1", item.Link)
	assert.Equal(t, root+"post/1/comments", item.Comments)
	require.NotNil(t, item.Source)
	assert.Equal(t, root+"src.xml", item.Source.URL)
	require.NotNil(t, item.Enclosure)
	assert.Equal(t, root+"media/a.mp3", item.Enclosure.URL)
	require.NotNil(t, item.GUID)
	// GUIDs are identifiers and stay exactly as authored.
	assert.Equal(t, "tag:example:1", item.GUID.Value)
}

// A document base without any in-document xml:base applies to the whole feed,
// and sibling items independently resolve against it.
func TestParser_ParseWithBase_NoInDocumentBase(t *testing.T) {
	doc := `<rss version="2.0"><channel>
	  <item><title>a</title><link>one</link></item>
	  <item><title>b</title><link>two</link></item>
	</channel></rss>`
	f := parseRSSWithBase(t, doc, "https://example.com/feed")

	require.Len(t, f.Items, 2)
	assert.Equal(t, "https://example.com/one", f.Items[0].Link)
	assert.Equal(t, "https://example.com/two", f.Items[1].Link)
}

// Nested xml:base values stack, and closing an element restores the sibling
// scope (the channel-level base must return after the item's base pops).
func TestParser_ParseWithBase_NestedAndSiblingScopes(t *testing.T) {
	doc := `<rss version="2.0"><channel xml:base="posts/">
	  <item xml:base="2026/"><link>a</link></item>
	  <item><link>b</link></item>
	</channel></rss>`
	f := parseRSSWithBase(t, doc, "https://example.com/feeds/")

	require.Len(t, f.Items, 2)
	assert.Equal(t, "https://example.com/feeds/posts/2026/a", f.Items[0].Link)
	assert.Equal(t, "https://example.com/feeds/posts/b", f.Items[1].Link)
}

// Body HTML (description and content:encoded) gets its URI attributes
// resolved only when a document base was supplied.
func TestParser_ParseWithBase_ResolvesBodyHTML(t *testing.T) {
	doc := `<rss version="2.0"><channel>
	  <description><![CDATA[<a href="/about">about</a><img src="logo.png"/>]]></description>
	  <item>
	    <title>p</title>
	    <description><![CDATA[<a href="detail">d</a>]]></description>
	    <content:encoded xmlns:content="http://purl.org/rss/1.0/modules/content/"><![CDATA[<video poster="p.jpg" src="v.mp4"></video>]]></content:encoded>
	  </item>
	</channel></rss>`
	f := parseRSSWithBase(t, doc, "https://example.com/feeds/")

	assert.Contains(t, f.Description, `href="https://example.com/about"`)
	assert.Contains(t, f.Description, `src="https://example.com/feeds/logo.png"`)
	item := f.Items[0]
	assert.Contains(t, item.Description, `href="https://example.com/feeds/detail"`)
	assert.Contains(t, item.Content, `poster="https://example.com/feeds/p.jpg"`)
	assert.Contains(t, item.Content, `src="https://example.com/feeds/v.mp4"`)
}

// Without a document base, body HTML is returned exactly as Parse would.
func TestParser_ParseWithBase_NilBaseLeavesBodyHTML(t *testing.T) {
	html := `<![CDATA[<a href="relative">x</a>]]>`
	doc := `<rss version="2.0"><channel><item><description>` + html +
		`</description></item></channel></rss>`
	f := parseRSSWithBase(t, doc, "")
	assert.Equal(t, `<a href="relative">x</a>`, f.Items[0].Description)
}

// References that fail to parse stay as written instead of breaking the parse
// or producing a half-resolved value.
func TestParser_ParseWithBase_UnparseableReferenceKept(t *testing.T) {
	doc := `<rss version="2.0"><channel>
	  <item><link>http://%zz</link><enclosure url="ok.mp3" type="audio/mpeg"/></item>
	</channel></rss>`
	f := parseRSSWithBase(t, doc, "https://example.com/feed")

	item := f.Items[0]
	assert.Equal(t, "http://%zz", item.Link)
	assert.Equal(t, "https://example.com/ok.mp3", item.Enclosure.URL)
}

// Parse and ParseWithBase(nil, nil) must produce identical output, so the new
// entry point cannot change behavior for callers that do not opt in.
func TestParser_ParseWithBase_NilEqualsParse(t *testing.T) {
	data := testutil.ReadFile(t, "../testdata/parser/rss/rss_channel_item_xml_base.xml")
	a, err := (&rss.Parser{}).Parse(strings.NewReader(string(data)))
	require.NoError(t, err)
	b, err := (&rss.Parser{}).ParseWithBase(strings.NewReader(string(data)), nil)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

// One Parser shared across goroutines with different document bases must keep
// each parse's resolutions isolated.
func TestParser_ParseWithBase_ConcurrentIsolation(t *testing.T) {
	doc := func(base string) string {
		return `<rss version="2.0"><channel><item><link>post</link>` +
			`<enclosure url="a.mp3" type="audio/mpeg"/></item></channel></rss>`
	}
	p := &rss.Parser{}
	done := make(chan error, 2)
	for _, base := range []string{"https://a.example/feeds/", "https://b.example/feeds/"} {
		base := base
		go func() {
			for i := 0; i < 50; i++ {
				f, err := p.ParseWithBase(strings.NewReader(doc(base)), mustURL(t, base))
				if err != nil {
					done <- err
					return
				}
				got := f.Items[0].Link
				want := base + "post"
				if got != want {
					done <- &isolationError{got: got, want: want}
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

type isolationError struct {
	got, want string
}

func (e *isolationError) Error() string { return "got " + e.got + ", want " + e.want }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// A reader error after the sniff window must still surface, and the body must
// not be silently truncated.
func TestParser_ParseWithBase_PropagatesReaderError(t *testing.T) {
	base := mustURL(t, "https://example.com/feed")
	_, err := (&rss.Parser{}).ParseWithBase(io.MultiReader(
		strings.NewReader(`<rss version="2.0"><channel><title>`),
		&failingReader{}), base)
	assert.Error(t, err)
}

type failingReader struct{}

func (*failingReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
