package gofeed_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/atom"
	"github.com/mmcdole/gofeed/rss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// translatorFunc adapts a plain function to the gofeed.Translator interface so
// tests can observe the source feed handed to a custom translator.
type translatorFunc func(feed interface{}) (*gofeed.Feed, error)

func (f translatorFunc) Translate(feed interface{}) (*gofeed.Feed, error) {
	return f(feed)
}

const relativeRSSFeed = `
<rss version="2.0">
  <channel>
    <title>Relative</title>
    <link>site/</link>
    <item>
      <title>Post</title>
      <link>posts/1</link>
      <guid>guid-1</guid>
      <enclosure url="audio/1.mp3" type="audio/mpeg" length="1"/>
    </item>
  </channel>
</rss>`

const relativeAtomFeed = `
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Relative</title>
  <id>urn:uuid:feed</id>
  <entry>
    <id>urn:uuid:entry</id>
    <link rel="alternate" href="posts/1"/>
    <content type="html">&lt;a href="details/1"&gt;x&lt;/a&gt;</content>
  </entry>
</feed>`

// With ResolveRelativeURLs off (the default), ParseStringWithBase must keep
// every URL exactly as written and ignore the supplied base.
func TestRelativeURLs_DisabledKeepsExistingBehavior(t *testing.T) {
	p := gofeed.NewParser()
	require.False(t, p.ResolveRelativeURLs)

	feed, err := p.ParseStringWithBase(relativeRSSFeed, "https://feed.example.com/feed/index.xml")
	require.NoError(t, err)
	require.Len(t, feed.Items, 1)

	assert.Equal(t, "site/", feed.Link)
	assert.Equal(t, "posts/1", feed.Items[0].Link)
	assert.Equal(t, "audio/1.mp3", feed.Items[0].Enclosures[0].URL)
	assert.Equal(t, "guid-1", feed.Items[0].GUID)
}

// With the option on and an explicit source URL, relative links and
// attachments resolve while the GUID stays verbatim.
func TestRelativeURLs_StringEntryWithSource(t *testing.T) {
	p := gofeed.NewParser()
	p.ResolveRelativeURLs = true

	feed, err := p.ParseStringWithBase(relativeRSSFeed, "https://feed.example.com/feed/index.xml")
	require.NoError(t, err)

	assert.Equal(t, "https://feed.example.com/feed/site/", feed.Link)
	require.Len(t, feed.Items, 1)
	assert.Equal(t, "https://feed.example.com/feed/posts/1", feed.Items[0].Link)
	require.Len(t, feed.Items[0].Enclosures, 1)
	assert.Equal(t, "https://feed.example.com/feed/audio/1.mp3", feed.Items[0].Enclosures[0].URL)
	// GUIDs are identifiers and must not be rewritten.
	assert.Equal(t, "guid-1", feed.Items[0].GUID)
}

// A malformed source URL is reported rather than silently ignored.
func TestRelativeURLs_InvalidSourceURL(t *testing.T) {
	p := gofeed.NewParser()
	p.ResolveRelativeURLs = true

	// Control characters make the reference unparseable.
	_, err := p.ParseStringWithBase(relativeRSSFeed, "https://feed.example.com/\x7f bad")
	require.Error(t, err)
}

// ParseURL must base relative references on the final URL after redirects,
// not on the originally requested URL.
func TestRelativeURLs_ParseURLUsesPostRedirectURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved/final/feed.xml", http.StatusFound)
	})
	mux.HandleFunc("/moved/final/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		io.WriteString(w, relativeRSSFeed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := gofeed.NewParser()
	p.ResolveRelativeURLs = true

	feed, err := p.ParseURL(srv.URL + "/old/feed.xml")
	require.NoError(t, err)
	require.Len(t, feed.Items, 1)

	// Resolved against /moved/final/feed.xml, not /old/feed.xml.
	assert.Equal(t, srv.URL+"/moved/final/site/", feed.Link)
	assert.Equal(t, srv.URL+"/moved/final/posts/1", feed.Items[0].Link)
	assert.Equal(t, srv.URL+"/moved/final/audio/1.mp3", feed.Items[0].Enclosures[0].URL)
	assert.Equal(t, "guid-1", feed.Items[0].GUID)
}

// With the option off, an HTTP parse leaves relative URLs untouched even
// though the response carries a usable final URL.
func TestRelativeURLs_ParseURLDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, relativeRSSFeed)
	}))
	defer srv.Close()

	feed, err := gofeed.NewParser().ParseURL(srv.URL + "/feed.xml")
	require.NoError(t, err)
	require.Len(t, feed.Items, 1)
	assert.Equal(t, "posts/1", feed.Items[0].Link)
	assert.Equal(t, "audio/1.mp3", feed.Items[0].Enclosures[0].URL)
}

// Relative resolution must also work on the MaxByteSize-limited response path.
func TestRelativeURLs_ParseURLWithMaxByteSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, relativeRSSFeed)
	}))
	defer srv.Close()

	p := gofeed.NewParser()
	p.ResolveRelativeURLs = true
	p.MaxByteSize = 1 << 20

	feed, err := p.ParseURL(srv.URL + "/feed.xml")
	require.NoError(t, err)
	require.Len(t, feed.Items, 1)
	assert.Equal(t, srv.URL+"/posts/1", feed.Items[0].Link)
	assert.Equal(t, srv.URL+"/audio/1.mp3", feed.Items[0].Enclosures[0].URL)
}

// The universal Feed, the retained OriginalFeed and a custom Translator must
// all observe the same resolved link values.
func TestRelativeURLs_OriginalFeedAndCustomTranslatorConsistency(t *testing.T) {
	t.Run("rss", func(t *testing.T) {
		var seenByCustom string
		p := gofeed.NewParser()
		p.ResolveRelativeURLs = true
		p.KeepOriginalFeed = true
		p.RSSTranslator = translatorFunc(func(feed interface{}) (*gofeed.Feed, error) {
			rf := feed.(*rss.Feed)
			seenByCustom = rf.Items[0].Link
			return (&gofeed.DefaultRSSTranslator{}).Translate(rf)
		})

		feed, err := p.ParseStringWithBase(relativeRSSFeed, "https://feed.example.com/feed/index.xml")
		require.NoError(t, err)

		want := "https://feed.example.com/feed/posts/1"
		assert.Equal(t, want, feed.Items[0].Link)
		assert.Equal(t, want, seenByCustom)

		orig, ok := feed.OriginalFeed().(*rss.Feed)
		require.True(t, ok)
		assert.Equal(t, want, orig.Items[0].Link)
		assert.Equal(t, "https://feed.example.com/feed/audio/1.mp3", orig.Items[0].Enclosures[0].URL)
		// The GUID stays raw at every layer.
		assert.Equal(t, "guid-1", orig.Items[0].GUID.Value)
	})

	t.Run("atom", func(t *testing.T) {
		var seenByCustom string
		p := gofeed.NewParser()
		p.ResolveRelativeURLs = true
		p.KeepOriginalFeed = true
		p.AtomTranslator = translatorFunc(func(feed interface{}) (*gofeed.Feed, error) {
			af := feed.(*atom.Feed)
			seenByCustom = af.Entries[0].Links[0].Href
			return (&gofeed.DefaultAtomTranslator{}).Translate(af)
		})

		feed, err := p.ParseStringWithBase(relativeAtomFeed, "https://feed.example.com/feed/index.xml")
		require.NoError(t, err)

		want := "https://feed.example.com/feed/posts/1"
		assert.Equal(t, want, feed.Items[0].Link)
		assert.Equal(t, want, seenByCustom)

		orig, ok := feed.OriginalFeed().(*atom.Feed)
		require.True(t, ok)
		assert.Equal(t, want, orig.Entries[0].Links[0].Href)
		// Atom id remains the raw identifier and the HTML href is resolved.
		assert.Equal(t, "urn:uuid:entry", orig.Entries[0].ID)
		assert.Contains(t, orig.Entries[0].Content.Value,
			`href="https://feed.example.com/feed/details/1"`)
	})
}

// One Parser shared across goroutines crawling different sources must resolve
// each feed against its own base with no cross-parse state leakage. Run with
// -race.
func TestRelativeURLs_ConcurrentDifferentSources(t *testing.T) {
	p := gofeed.NewParser()
	p.ResolveRelativeURLs = true

	sources := []string{
		"https://a.example.com/feeds/feed.xml",
		"https://b.example.com/path/to/feed.xml",
		"https://c.example.com/deeply/nested/feed/index.xml",
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := sources[i%len(sources)]
			base, err := url.Parse(src)
			if err != nil {
				t.Error(err)
				return
			}
			feed, err := p.ParseStringWithBase(relativeRSSFeed, src)
			if err != nil {
				t.Error(err)
				return
			}
			want := base.ResolveReference(mustURL(t, "posts/1")).String()
			if got := feed.Items[0].Link; got != want {
				t.Errorf("iteration %d: link = %q, want %q", i, got, want)
			}
			if feed.Items[0].GUID != "guid-1" {
				t.Errorf("iteration %d: guid rewritten to %q", i, feed.Items[0].GUID)
			}
		}(i)
	}
	wg.Wait()
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}
