package upstream

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base *url.URL
	http *http.Client
}

func New(base *url.URL, timeout time.Duration) *Client {
	return &Client{
		base: base,
		http: &http.Client{
			Timeout: timeout,
			// Never follow a redirect to an address outside the configured newapi origin.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Client) Do(ctx context.Context, in *http.Request, body io.Reader) (*http.Response, error) {
	target := *c.base
	target.Path = joinPath(c.base.Path, in.URL.Path)
	target.RawQuery = in.URL.RawQuery
	req, err := http.NewRequestWithContext(ctx, in.Method, target.String(), body)
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, in.Header)
	req.Header.Set("Accept-Encoding", "identity")
	if in.ContentLength >= 0 {
		req.ContentLength = in.ContentLength
	}
	return c.http.Do(req)
}

func joinPath(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

var hopHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true, "host": true,
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if hopHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func CopyResponseHeaders(dst, src http.Header) { copyHeaders(dst, src) }
