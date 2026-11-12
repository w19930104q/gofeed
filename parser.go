package gofeed

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed/atom"
	"github.com/mmcdole/gofeed/json"
	"github.com/mmcdole/gofeed/rss"
)

// defaultRequestTimeout bounds ParseURL, which would otherwise use a client
// with no timeout and hang forever on a slow or unresponsive server.
// ParseURLWithContext callers manage their own context and are not subject to
// this.
const defaultRequestTimeout = 30 * time.Second

// ErrResponseTooLarge is returned when a response body exceeds Parser.MaxByteSize.
var ErrResponseTooLarge = errors.New("gofeed: response body exceeds MaxByteSize")

// ErrFeedTypeNotDetected is returned when the detection system can not figure
// out the Feed format
var ErrFeedTypeNotDetected = errors.New("failed to detect feed type")

// ErrBaseURLNotAbsolute is returned by the base-aware entry points when the
// document base URL is neither empty nor absolute. Relative references can
// only be resolved against an absolute base.
var ErrBaseURLNotAbsolute = errors.New("gofeed: base url must be absolute")

// HTTPError represents an HTTP error returned by a server.
type HTTPError struct {
	StatusCode int
	Status     string
}

// Error returns the string representation of the HTTP error.
func (err HTTPError) Error() string {
	return fmt.Sprintf("http error: %s", err.Status)
}

// Parser is a universal feed parser that detects
// a given feed type, parsers it, and translates it
// to the universal feed type.
type Parser struct {
	AtomTranslator Translator
	RSSTranslator  Translator
	JSONTranslator Translator
	UserAgent      string
	AuthConfig     *Auth
	Client         *http.Client
	// MaxByteSize limits how many bytes ParseURL/ParseURLWithContext will read
	// from a response body. Zero means no limit. Exceeding it returns
	// ErrResponseTooLarge rather than silently truncating.
	MaxByteSize int64
	// KeepOriginalFeed retains the source rss/atom/json feed on the result,
	// accessible via Feed.OriginalFeed(). Off by default: keeping it holds a
	// second copy of the feed in memory for the lifetime of the result.
	KeepOriginalFeed bool
	rp               *rss.Parser
	ap               *atom.Parser
	jp               *json.Parser
}

// Auth is a structure allowing to
// use the BasicAuth during the HTTP request
// It must be instantiated with your new Parser
type Auth struct {
	Username string
	Password string
}

// Shared defaults used when the corresponding Parser field is unset. The
// default translators are stateless and http.Client is safe for concurrent
// use, so single shared instances are fine and avoid per-parse allocation.
var (
	defaultAtomTranslator = &DefaultAtomTranslator{}
	defaultRSSTranslator  = &DefaultRSSTranslator{}
	defaultJSONTranslator = &DefaultJSONTranslator{}
	defaultClient         = &http.Client{}
)

// NewParser creates a universal feed parser.
func NewParser() *Parser {
	fp := Parser{
		rp:        &rss.Parser{},
		ap:        &atom.Parser{},
		jp:        &json.Parser{},
		UserAgent: "Gofeed/1.0",
	}
	return &fp
}

// detectionPeekSize is how many leading bytes Parse inspects to detect the
// feed type. The root element appears within the first few KB of any real
// feed; bounding the peek lets the XML parsers stream the rest of the input
// instead of the whole feed being buffered just for detection. A feed whose
// root element starts beyond this window is not detected.
const detectionPeekSize = 4096

// Parse parses a RSS or Atom or JSON feed into
// the universal gofeed.Feed.  It takes an
// io.Reader which should return the xml/json content.
//
// Only the first few KB are buffered to detect the feed type; RSS and Atom
// content is then parsed incrementally from the reader. JSON feeds are read
// fully into memory, as JSON decoding needs the complete document.
func (f *Parser) Parse(feed io.Reader) (*Feed, error) {
	return f.parse(feed, nil)
}

// ParseWithBase behaves like Parse but treats base as the document base URL:
// relative links in the feed are resolved against it and come back absolute,
// ready to use. Base must be absolute (or nil, which is equivalent to Parse).
// In-document xml:base declarations still take effect on top of it.
// Identifiers (Atom/RSS IDs and GUIDs, JSON item ids), plain text and
// references that fail to parse are left exactly as authored, and no derived
// link is ever requested over the network.
func (f *Parser) ParseWithBase(feed io.Reader, base *url.URL) (*Feed, error) {
	if err := validateBase(base); err != nil {
		return nil, err
	}
	return f.parse(feed, base)
}

func (f *Parser) parse(feed io.Reader, base *url.URL) (*Feed, error) {
	// Peek at the start of the stream to detect the feed type, without
	// consuming it: the format parser below reads from the beginning. A
	// reader error here surfaces as itself rather than as a failed type
	// detection; io.EOF just means the whole feed fit inside the window.
	br := bufio.NewReaderSize(feed, detectionPeekSize)
	prefix, err := br.Peek(detectionPeekSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	switch DetectFeedType(bytes.NewReader(prefix)) {
	case FeedTypeAtom:
		return f.parseAtomFeed(br, base)
	case FeedTypeRSS:
		return f.parseRSSFeed(br, base)
	case FeedTypeJSON:
		return f.parseJSONFeed(br, base)
	}

	return nil, ErrFeedTypeNotDetected
}

// validateBase rejects a non-nil relative base: RFC 3986 reference
// resolution needs an absolute base to produce a usable absolute URL.
func validateBase(base *url.URL) error {
	if base != nil && !base.IsAbs() {
		return ErrBaseURLNotAbsolute
	}
	return nil
}

// ParseURL fetches the contents of a given url and
// attempts to parse the response into the universal feed type. It applies a
// default request timeout; use ParseURLWithContext to control cancellation.
func (f *Parser) ParseURL(feedURL string) (feed *Feed, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
	defer cancel()
	return f.ParseURLWithContext(feedURL, ctx)
}

// ParseURLResolved fetches the contents of a given url, parses the response
// into the universal feed type, and resolves every relative link against the
// final response URL — the URL after redirects, obtained from resp.Request.URL.
// It applies the same default timeout as ParseURL.
func (f *Parser) ParseURLResolved(feedURL string) (feed *Feed, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
	defer cancel()
	return f.ParseURLResolvedWithContext(feedURL, ctx)
}

// ParseURLWithContext fetches contents of a given url and
// attempts to parse the response into the universal feed type.
// You can instantiate the Auth structure with your Username and Password
// to use the BasicAuth during the HTTP call.
// It will be automatically added to the header of the request
// Request could be canceled or timeout via given context
func (f *Parser) ParseURLWithContext(feedURL string, ctx context.Context) (feed *Feed, err error) {
	return f.fetchAndParse(feedURL, ctx, nil)
}

// ParseURLResolvedWithContext is the cancellable form of ParseURLResolved:
// relative links resolve against the final response URL (resp.Request.URL),
// which already reflects any redirects the configured Client followed.
func (f *Parser) ParseURLResolvedWithContext(feedURL string, ctx context.Context) (feed *Feed, err error) {
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		return nil, ErrBaseURLNotAbsolute
	}
	return f.fetchAndParse(feedURL, ctx, parsed)
}

// fetchAndParse performs the HTTP request and parses the body. When baseHint
// is non-nil, resolution is enabled and the final response URL (which may
// differ from both the hint and the requested URL after a redirect) is used
// as the document base. A nil hint keeps the historical, non-resolving
// behavior.
func (f *Parser) fetchAndParse(feedURL string, ctx context.Context, baseHint *url.URL) (feed *Feed, err error) {
	client := f.httpClient()

	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)

	if f.AuthConfig != nil && f.AuthConfig.Username != "" && f.AuthConfig.Password != "" {
		req.SetBasicAuth(f.AuthConfig.Username, f.AuthConfig.Password)
	}

	resp, err := client.Do(req)

	if err != nil {
		return nil, err
	}

	if resp != nil {
		defer func() {
			if ce := resp.Body.Close(); ce != nil && err == nil {
				err = ce
			}
		}()
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, HTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
		}
	}

	// Resolution, when requested, keys off the final response URL so
	// redirects to another host or directory resolve correctly.
	var base *url.URL
	if baseHint != nil {
		base = resp.Request.URL
		if base == nil || !base.IsAbs() {
			base = baseHint
		}
	}

	var body io.Reader = resp.Body
	if f.MaxByteSize > 0 {
		body = &limitedReader{r: resp.Body, left: f.MaxByteSize}
	}
	feed, err = f.parse(body, base)
	if lerr, ok := body.(*limitedReader); ok && lerr.err == ErrResponseTooLarge {
		return nil, ErrResponseTooLarge
	}
	if err != nil {
		return nil, err
	}
	if lerr, ok := body.(*limitedReader); ok {
		// XML parsers stop at the root's closing tag. Check the remaining body
		// through the same limit before returning success, including any error
		// that a buffered parser has not observed yet.
		if _, err = io.Copy(io.Discard, lerr); err != nil {
			return nil, err
		}
	}
	return feed, nil
}

// limitedReader returns ErrResponseTooLarge once more than the configured
// number of bytes has been read, rather than silently truncating (which would
// produce a corrupt partial parse).
type limitedReader struct {
	r    io.Reader
	left int64
	err  error
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.err != nil {
		return 0, l.err
	}
	// Read at most one byte beyond the limit to distinguish overflow from
	// an exact-size response. The comparison also keeps left+1 from overflowing.
	if int64(len(p)) > l.left {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	if int64(n) > l.left {
		n = int(l.left)
		err = ErrResponseTooLarge
	}
	l.left -= int64(n)
	l.err = err
	return n, err
}

// ParseString parses a feed XML string and into the
// universal feed type.
func (f *Parser) ParseString(feed string) (*Feed, error) {
	return f.Parse(strings.NewReader(feed))
}

// ParseStringWithBase is ParseString with an explicit document base URL,
// behaving as ParseWithBase does for readers.
func (f *Parser) ParseStringWithBase(feed string, base *url.URL) (*Feed, error) {
	return f.ParseWithBase(strings.NewReader(feed), base)
}

func (f *Parser) parseAtomFeed(feed io.Reader, base *url.URL) (*Feed, error) {
	af, err := f.ap.ParseWithBase(feed, base)
	if err != nil {
		return nil, err
	}
	result, err := f.atomTrans().Translate(af)
	f.keepOriginal(result, af)
	return result, err
}

func (f *Parser) parseRSSFeed(feed io.Reader, base *url.URL) (*Feed, error) {
	rf, err := f.rp.ParseWithBase(feed, base)
	if err != nil {
		return nil, err
	}

	result, err := f.rssTrans().Translate(rf)
	f.keepOriginal(result, rf)
	return result, err
}

func (f *Parser) parseJSONFeed(feed io.Reader, base *url.URL) (*Feed, error) {
	jf, err := f.jp.ParseWithBase(feed, base)
	if err != nil {
		return nil, err
	}
	result, err := f.jsonTrans().Translate(jf)
	f.keepOriginal(result, jf)
	return result, err
}

// keepOriginal stashes the source feed on the result when KeepOriginalFeed is
// set. Gating here keeps the Translator interface free of parse options.
func (f *Parser) keepOriginal(result *Feed, original interface{}) {
	if f.KeepOriginalFeed && result != nil {
		result.originalFeed = original
	}
}

// These accessors return a shared default when the corresponding field is
// unset. They must not write back to the Parser: doing so races when one Parser
// is shared across goroutines (a common pattern for crawlers).

func (f *Parser) atomTrans() Translator {
	if f.AtomTranslator != nil {
		return f.AtomTranslator
	}
	return defaultAtomTranslator
}

func (f *Parser) rssTrans() Translator {
	if f.RSSTranslator != nil {
		return f.RSSTranslator
	}
	return defaultRSSTranslator
}

func (f *Parser) jsonTrans() Translator {
	if f.JSONTranslator != nil {
		return f.JSONTranslator
	}
	return defaultJSONTranslator
}

func (f *Parser) httpClient() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return defaultClient
}
