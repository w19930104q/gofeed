package json_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed/internal/testutil"
	jsonParser "github.com/mmcdole/gofeed/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseJSONWithBase(t *testing.T, doc, base string) *jsonParser.Feed {
	t.Helper()
	var u *url.URL
	if base != "" {
		var err error
		u, err = url.Parse(base)
		require.NoError(t, err)
	}
	f, err := (&jsonParser.Parser{}).ParseWithBase(strings.NewReader(doc), u)
	require.NoError(t, err)
	return f
}

const docBaseFeed = `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "J",
  "home_page_url": "site/index.html",
  "feed_url": "feed.json",
  "next_url": "p2.json",
  "icon": "art/icon.png",
  "favicon": "art/favicon.ico",
  "authors": [{"name": "Jane", "url": "people/jane", "avatar": "img/jane.png"}],
  "items": [
    {
      "id": "tag:42",
      "url": "posts/1",
      "external_url": "https://external.test/x",
      "image": "img/1.png",
      "banner_image": "img/banner.png",
      "content_html": "<p><a href=\"deep\">go</a><img src=\"pic.png\"></p>",
      "content_text": "plain text stays",
      "summary": "a summary",
      "authors": [{"name": "Bob", "url": "people/bob", "avatar": "img/bob.png"}],
      "attachments": [
        {"url": "media/a.mp3", "mime_type": "audio/mpeg", "size_in_bytes": 12}
      ]
    }
  ]
}`

func TestParser_ParseWithBase_ResolvesAllURLFields(t *testing.T) {
	f := parseJSONWithBase(t, docBaseFeed, "https://example.com/feeds/feed.json")
	const base = "https://example.com/feeds/"

	assert.Equal(t, base+"site/index.html", f.HomePageURL)
	assert.Equal(t, base+"feed.json", f.FeedURL)
	assert.Equal(t, base+"p2.json", f.NextURL)
	assert.Equal(t, base+"art/icon.png", f.Icon)
	assert.Equal(t, base+"art/favicon.ico", f.Favicon)
	require.Len(t, f.Authors, 1)
	assert.Equal(t, base+"people/jane", f.Authors[0].URL)
	assert.Equal(t, base+"img/jane.png", f.Authors[0].Avatar)

	require.Len(t, f.Items, 1)
	item := f.Items[0]
	// Item ID is an identifier and stays untouched even when relative.
	assert.Equal(t, "tag:42", item.ID)
	assert.Equal(t, base+"posts/1", item.URL)
	// An absolute external URL is unchanged by resolution.
	assert.Equal(t, "https://external.test/x", item.ExternalURL)
	assert.Equal(t, base+"img/1.png", item.Image)
	assert.Equal(t, base+"img/banner.png", item.BannerImage)
	assert.Contains(t, item.ContentHTML, `href="https://example.com/feeds/deep"`)
	assert.Contains(t, item.ContentHTML, `src="https://example.com/feeds/pic.png"`)
	// Plain text never passes through the HTML resolver.
	assert.Equal(t, "plain text stays", item.ContentText)
	assert.Equal(t, "a summary", item.Summary)
	require.Len(t, item.Authors, 1)
	assert.Equal(t, base+"people/bob", item.Authors[0].URL)
	assert.Equal(t, base+"img/bob.png", item.Authors[0].Avatar)
	require.NotNil(t, item.Attachments)
	require.Len(t, *item.Attachments, 1)
	assert.Equal(t, base+"media/a.mp3", (*item.Attachments)[0].URL)
}

// Legacy singular author fields resolve too.
func TestParser_ParseWithBase_ResolvesSingularAuthor(t *testing.T) {
	doc := `{"version":"https://jsonfeed.org/version/1","title":"t",
	  "author":{"name":"A","url":"u","avatar":"av.png"},
	  "items":[{"id":"1","author":{"name":"B","url":"ub","avatar":"b.png"}}]}`
	f := parseJSONWithBase(t, doc, "https://example.com/f.json")
	const base = "https://example.com/"
	require.NotNil(t, f.Author)
	assert.Equal(t, base+"u", f.Author.URL)
	assert.Equal(t, base+"av.png", f.Author.Avatar)
	require.NotNil(t, f.Items[0].Author)
	assert.Equal(t, base+"ub", f.Items[0].Author.URL)
	assert.Equal(t, base+"b.png", f.Items[0].Author.Avatar)
}

// An unparseable reference remains as authored; valid fields still resolve.
func TestParser_ParseWithBase_UnparseableReferenceKept(t *testing.T) {
	doc := `{"version":"https://jsonfeed.org/version/1.1","title":"t",
	  "items":[{"id":"id-1","url":"http://%zz","image":"ok.png",
	  "content_html":"<a href=\"http://[bad\">x</a><img src=\"g.png\">",
	  "attachments":[{"url":"a.mp3","mime_type":"audio/mpeg"}]}]}`
	f := parseJSONWithBase(t, doc, "https://example.com/feeds/")
	item := f.Items[0]
	assert.Equal(t, "http://%zz", item.URL)
	assert.Equal(t, "https://example.com/feeds/ok.png", item.Image)
	assert.Contains(t, item.ContentHTML, `href="http://[bad"`)
	assert.Contains(t, item.ContentHTML, `src="https://example.com/feeds/g.png"`)
	assert.Equal(t, "https://example.com/feeds/a.mp3", (*item.Attachments)[0].URL)
}

func TestParser_ParseWithBase_NilEqualsParse(t *testing.T) {
	data := testutil.ReadFile(t, "../testdata/parser/json/version_json_10.json")
	a, err := (&jsonParser.Parser{}).Parse(strings.NewReader(string(data)))
	require.NoError(t, err)
	b, err := (&jsonParser.Parser{}).ParseWithBase(strings.NewReader(string(data)), nil)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestParser_ParseWithBase_ConcurrentIsolation(t *testing.T) {
	doc := `{"version":"https://jsonfeed.org/version/1.1","title":"t",
	  "items":[{"id":"1","url":"post","attachments":[{"url":"a","mime_type":"audio/mpeg"}]}]}`
	p := &jsonParser.Parser{}
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
				if got := f.Items[0].URL; got != base+"post" {
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
