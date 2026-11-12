package gofeed_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mmcdole/gofeed"
	"github.com/stretchr/testify/require"
)

// Chunked, one-byte and half-token readers must not break the sniff-then-
// replay stream construction used for base resolution.
func TestParseWithBase_ChunkedReaders(t *testing.T) {
	docs := map[string]string{
		"rss":  rssRelativeDoc,
		"atom": atomRelativeDoc,
		"json": jsonRelativeDoc,
	}
	base, err := url.Parse("https://example.com/feeds/f.xml")
	require.NoError(t, err)

	readers := map[string]func(io.Reader) io.Reader{
		"onebyte": iotest.OneByteReader,
		"half":    iotest.HalfReader,
		"dataerr": iotest.DataErrReader,
	}
	p := gofeed.NewParser()
	for format, doc := range docs {
		for name, wrap := range readers {
			got, err := p.ParseWithBase(wrap(strings.NewReader(doc)), base)
			require.NoErrorf(t, err, "%s via %s", format, name)
			require.NotEmptyf(t, got.Items, "%s via %s", format, name)
		}
	}
}
