package gofeed_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/atom"
	jsonfeed "github.com/mmcdole/gofeed/json"
	"github.com/mmcdole/gofeed/rss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rssRelativeDoc = `<rss version="2.0"><channel><title>r</title>
	  <link>index.html</link>
	  <item><title>i</title><link>post/1</link>
	    <enclosure url="a.mp3" type="audio/mpeg"/></item>
	</channel></rss>`

	atomRelativeDoc = `<feed xmlns="http://www.w3.org/2005/Atom"><title>a</title>
	  <link href="alt.xml" rel="alternate"/>
	  <entry><link href="one.html"/></entry></feed>`

	jsonRelativeDoc = `{"version":"https://jsonfeed.org/version/1.1","title":"j",
	  "home_page_url":"index.html","feed_url":"feed.json",
	  "items":[{"id":"id-1","url":"post/1"}]}`
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestParseWithBase_AllFormats(t *testing.T) {
	p := gofeed.NewParser()
	base := mustParseURL(t, "https://example.com/feeds/feed.xml")

	t.Run("rss", func(t *testing.T) {
		f, err := p.ParseWithBase(strings.NewReader(rssRelativeDoc), base)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/feeds/index.html", f.Link)
		require.Len(t, f.Items, 1)
		assert.Equal(t, "https://example.com/feeds/post/1", f.Items[0].Link)
		assert.Equal(t, "https://example.com/feeds/a.mp3", f.Items[0].Enclosures[0].URL)
	})

	t.Run("atom", func(t *testing.T) {
		f, err := p.ParseWithBase(strings.NewReader(atomRelativeDoc), base)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/feeds/alt.xml", f.Link)
		require.Len(t, f.Items, 1)
		assert.Equal(t, "https://example.com/feeds/one.html", f.Items[0].Link)
	})

	t.Run("json", func(t *testing.T) {
		f, err := p.ParseWithBase(strings.NewReader(jsonRelativeDoc), base)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/feeds/index.html", f.Link)
		assert.Equal(t, "https://example.com/feeds/feed.json", f.FeedLink)
		require.Len(t, f.Items, 1)
		assert.Equal(t, "https://example.com/feeds/post/1", f.Items[0].Link)
		// The item ID is not a reference and must not be resolved.
		assert.Equal(t, "id-1", f.Items[0].GUID)
	})
}

func TestParseStringWithBase(t *testing.T) {
	p := gofeed.NewParser()
	f, err := p.ParseStringWithBase(rssRelativeDoc, mustParseURL(t, "https://example.com/feeds/"))
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/feeds/index.html", f.Link)
}

func TestParseWithBase_RejectsRelativeBase(t *testing.T) {
	p := gofeed.NewParser()
	_, err := p.ParseWithBase(strings.NewReader(rssRelativeDoc), mustParseURL(t, "feeds/rel.xml"))
	require.ErrorIs(t, err, gofeed.ErrBaseURLNotAbsolute)

	// nil remains valid and means "no base".
	_, err = p.ParseWithBase(strings.NewReader(rssRelativeDoc), nil)
	require.NoError(t, err)
}

// Parse (no base) never resolves relative URLs, even after base-aware parses
// have run on the same Parser.
func TestParseWithBase_LegacyParseUntouched(t *testing.T) {
	p := gofeed.NewParser()
	base := mustParseURL(t, "https://example.com/feeds/feed.xml")
	resolved, err := p.ParseWithBase(strings.NewReader(rssRelativeDoc), base)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/feeds/index.html", resolved.Link)

	legacy, err := p.Parse(strings.NewReader(rssRelativeDoc))
	require.NoError(t, err)
	assert.Equal(t, "index.html", legacy.Link)
	assert.Equal(t, "post/1", legacy.Items[0].Link)
}

// A custom translator and OriginalFeed must observe the same already-resolved
// source object, with the Translator interface unchanged.
func TestParseWithBase_CustomTranslatorAndOriginalFeed(t *testing.T) {
	var seen *rss.Feed
	trans := translatorFunc(func(v interface{}) (*gofeed.Feed, error) {
		seen = v.(*rss.Feed)
		return (&gofeed.DefaultRSSTranslator{}).Translate(v)
	})

	p := gofeed.NewParser()
	p.RSSTranslator = trans
	p.KeepOriginalFeed = true

	f, err := p.ParseWithBase(strings.NewReader(rssRelativeDoc),
		mustParseURL(t, "https://example.com/feeds/feed.xml"))
	require.NoError(t, err)

	require.NotNil(t, seen)
	assert.Equal(t, "https://example.com/feeds/index.html", seen.Link)
	assert.Equal(t, "https://example.com/feeds/post/1", seen.Items[0].Link)

	original, ok := f.OriginalFeed().(*rss.Feed)
	require.True(t, ok)
	// Same object the translator received.
	assert.Same(t, seen, original)
	assert.Equal(t, "https://example.com/feeds/post/1", original.Items[0].Link)
}

// Redirects: ParseURLResolved must use the final response URL, not the
// originally requested one.
func TestParseURLResolved_UsesFinalURLAfterRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old/feed", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new/location/feed.xml", http.StatusFound)
	})
	mux.HandleFunc("/new/location/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		io.WriteString(w, rssRelativeDoc)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := gofeed.NewParser()
	f, err := p.ParseURLResolved(srv.URL + "/old/feed")
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/new/location/index.html", f.Link)
	require.Len(t, f.Items, 1)
	assert.Equal(t, srv.URL+"/new/location/post/1", f.Items[0].Link)
}

// All three formats through the HTTP entry point.
func TestParseURLResolved_AllFormats(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		wantLink    string
	}{
		{"rss", "application/rss+xml", rssRelativeDoc, "/d/index.html"},
		{"atom", "application/atom+xml", atomRelativeDoc, "/d/alt.xml"},
		{"json", "application/feed+json", jsonRelativeDoc, "/d/index.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			f, err := gofeed.NewParser().ParseURLResolved(srv.URL + "/d/feed")
			require.NoError(t, err)
			assert.Equal(t, srv.URL+tc.wantLink, f.Link)
		})
	}
}

// The non-resolving HTTP entry point keeps returning raw relative URLs.
func TestParseURL_LegacyBehaviorKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, rssRelativeDoc)
	}))
	defer srv.Close()

	f, err := gofeed.NewParser().ParseURL(srv.URL + "/feed")
	require.NoError(t, err)
	assert.Equal(t, "index.html", f.Link)
}

// The response body budget still applies when resolution is enabled.
func TestParseURLResolved_RespectsMaxByteSize(t *testing.T) {
	big := `<rss version="2.0"><channel><title>` + strings.Repeat("x", 200000) +
		`</title></channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, big)
	}))
	defer srv.Close()

	p := gofeed.NewParser()
	p.MaxByteSize = 1000
	_, err := p.ParseURLResolved(srv.URL + "/feed")
	require.ErrorIs(t, err, gofeed.ErrResponseTooLarge)
}

// Cancellation still works on the resolving HTTP entry point.
func TestParseURLResolved_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := gofeed.NewParser().ParseURLResolvedWithContext(srv.URL+"/feed", ctx)
	require.Error(t, err)
	assert.Less(t, time.Since(start), time.Second)
}

func TestParseURLResolved_RejectsRelativeURL(t *testing.T) {
	_, err := gofeed.NewParser().ParseURLResolved("not-an-absolute-url")
	require.ErrorIs(t, err, gofeed.ErrBaseURLNotAbsolute)
}

// One Parser, many goroutines, different bases: resolutions must never cross.
func TestParseWithBase_ConcurrentDifferentBases(t *testing.T) {
	p := gofeed.NewParser()
	bases := []string{
		"https://a.example/feeds/",
		"https://b.example/feeds/",
		"https://c.example/feeds/",
		"https://d.example/feeds/",
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(bases)*25)
	for _, b := range bases {
		b := b
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				docs := []string{rssRelativeDoc, atomRelativeDoc, jsonRelativeDoc}
				for _, doc := range docs {
					f, err := p.ParseWithBase(strings.NewReader(doc), mustParseURL(t, b))
					if err != nil {
						errCh <- err
						return
					}
					if !strings.HasPrefix(f.Link, b) {
						errCh <- assert.AnError
						return
					}
				}
			}()
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

// The specialized parsers' ParseWithBase also expose resolved source objects
// through OriginalFeed for Atom and JSON, not only RSS.
func TestParseWithBase_OriginalFeedAllFormats(t *testing.T) {
	p := gofeed.NewParser()
	p.KeepOriginalFeed = true
	base := mustParseURL(t, "https://example.com/feeds/f.xml")

	af, err := p.ParseWithBase(strings.NewReader(atomRelativeDoc), base)
	require.NoError(t, err)
	aOrig, ok := af.OriginalFeed().(*atom.Feed)
	require.True(t, ok)
	assert.Equal(t, "https://example.com/feeds/alt.xml", aOrig.Links[0].Href)

	jf, err := p.ParseWithBase(strings.NewReader(jsonRelativeDoc), base)
	require.NoError(t, err)
	jOrig, ok := jf.OriginalFeed().(*jsonfeed.Feed)
	require.True(t, ok)
	assert.Equal(t, "https://example.com/feeds/feed.json", jOrig.FeedURL)
	assert.Equal(t, "id-1", jOrig.Items[0].ID)
}

// translatorFunc adapts a function into the Translator interface for tests.
type translatorFunc func(interface{}) (*gofeed.Feed, error)

func (f translatorFunc) Translate(feed interface{}) (*gofeed.Feed, error) {
	return f(feed)
}
