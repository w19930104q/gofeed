package json

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"

	"github.com/mmcdole/gofeed/internal/shared"
)

// Parser is an JSON Feed Parser
type Parser struct{}

// Parse parses an json feed into an json.Feed
func (ap *Parser) Parse(feed io.Reader) (*Feed, error) {
	return ap.ParseWithBase(feed, nil)
}

// ParseWithBase parses a json feed into a json.Feed, resolving relative URL
// fields against base. A nil base preserves Parse's behavior exactly. Item
// IDs are identifiers rather than references and stay untouched; values that
// url.Parse rejects are also kept as written. No reference is ever fetched.
// The base is an argument, not Parser state, so one Parser can parse feeds
// with different bases concurrently.
func (ap *Parser) ParseWithBase(feed io.Reader, base *url.URL) (*Feed, error) {
	jsonFeed := &Feed{}

	buffer := new(bytes.Buffer)
	if _, err := buffer.ReadFrom(feed); err != nil {
		return nil, err
	}

	if err := json.Unmarshal(buffer.Bytes(), jsonFeed); err != nil {
		return nil, err
	}

	if base != nil {
		resolveFeedURLs(jsonFeed, base)
	}
	return jsonFeed, nil
}

// resolveFeedURLs rewrites every URL-bearing field of the feed in place
// against base. Plain-text fields (titles, descriptions, IDs, dates, tags)
// are never touched.
func resolveFeedURLs(f *Feed, base *url.URL) {
	f.HomePageURL = shared.ResolveURLIfBase(base, f.HomePageURL)
	f.FeedURL = shared.ResolveURLIfBase(base, f.FeedURL)
	f.NextURL = shared.ResolveURLIfBase(base, f.NextURL)
	f.Icon = shared.ResolveURLIfBase(base, f.Icon)
	f.Favicon = shared.ResolveURLIfBase(base, f.Favicon)
	resolveAuthorURLs(f.Author, base)
	for _, a := range f.Authors {
		resolveAuthorURLs(a, base)
	}
	for _, item := range f.Items {
		resolveItemURLs(item, base)
	}
}

func resolveItemURLs(item *Item, base *url.URL) {
	// ID stays as authored: it is an identifier, even when it happens to look
	// like a relative URL.
	item.URL = shared.ResolveURLIfBase(base, item.URL)
	item.ExternalURL = shared.ResolveURLIfBase(base, item.ExternalURL)
	item.Image = shared.ResolveURLIfBase(base, item.Image)
	item.BannerImage = shared.ResolveURLIfBase(base, item.BannerImage)
	if item.ContentHTML != "" {
		if resolved, err := shared.ResolveHTML(base, item.ContentHTML); err == nil {
			item.ContentHTML = resolved
		}
	}
	resolveAuthorURLs(item.Author, base)
	for _, a := range item.Authors {
		resolveAuthorURLs(a, base)
	}
	if item.Attachments != nil {
		for i := range *item.Attachments {
			(*item.Attachments)[i].URL =
				shared.ResolveURLIfBase(base, (*item.Attachments)[i].URL)
		}
	}
}

func resolveAuthorURLs(author *Author, base *url.URL) {
	if author == nil {
		return
	}
	author.URL = shared.ResolveURLIfBase(base, author.URL)
	author.Avatar = shared.ResolveURLIfBase(base, author.Avatar)
}
