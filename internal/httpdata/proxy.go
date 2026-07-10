package httpdata

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"image-gateway-middleware/internal/requestbody"
	"image-gateway-middleware/internal/upstream"
)

type ImageResult struct {
	Audit       requestbody.Audit
	RawResponse []byte
	Response    *http.Response
	Started     time.Time
}
type ImageProcessor interface {
	Process(http.ResponseWriter, *http.Request, ImageResult)
}
type Proxy struct {
	client               *upstream.Client
	maxJSON, maxResponse int64
	processor            ImageProcessor
	preflight            func(context.Context) error
}

func NewProxy(client *upstream.Client, maxJSON, maxResponse int64, processor ImageProcessor, preflight ...func(context.Context) error) *Proxy {
	p := &Proxy{client: client, maxJSON: maxJSON, maxResponse: maxResponse, processor: processor}
	if len(preflight) > 0 {
		p.preflight = preflight[0]
	}
	return p
}
func (p *Proxy) Models(w http.ResponseWriter, r *http.Request) {
	resp, err := p.client.Do(r.Context(), r, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "upstream request failed")
		return
	}
	defer resp.Body.Close()
	raw, err := readLimited(resp.Body, p.maxResponse)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_response_error", "upstream response is unavailable")
		return
	}
	upstream.CopyResponseHeaders(w.Header(), resp.Header)
	removeBodyIntegrityHeaders(w.Header())
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}
func removeBodyIntegrityHeaders(header http.Header) {
	for _, name := range []string{"Content-Length", "Content-Encoding", "ETag", "Digest", "Content-MD5"} {
		header.Del(name)
	}
}

func (p *Proxy) Image(w http.ResponseWriter, r *http.Request, isEdit bool) {
	started := time.Now()
	if p.preflight != nil {
		if err := p.preflight(r.Context()); err != nil {
			writeError(w, http.StatusInsufficientStorage, "insufficient_storage", err.Error())
			return
		}
	}
	var audit requestbody.Audit
	var body io.Reader
	var observed <-chan requestbody.Audit
	if isEdit {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			writeError(w, http.StatusBadRequest, "invalid_multipart", "multipart/form-data with boundary required")
			return
		}
		wrapped, ch := requestbody.ObserveMultipart(r.Body, params["boundary"])
		body = wrapped
		observed = ch
	} else {
		var err error
		audit, err = requestbody.ReadJSON(r.Body, p.maxJSON)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		body = bytes.NewReader(audit.Raw)
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	resp, err := p.client.Do(ctx, r, body)
	if isEdit {
		select {
		case a := <-observed:
			audit = a
		case <-ctx.Done():
			audit.Err = ctx.Err()
		}
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "upstream request failed")
		return
	}
	defer resp.Body.Close()
	raw, err := readLimited(resp.Body, p.maxResponse)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_response_error", "upstream response is unavailable")
		return
	}
	p.processor.Process(w, r, ImageResult{Audit: audit, RawResponse: raw, Response: resp, Started: started})
}
func readLimited(r io.Reader, max int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", max)
	}
	return raw, nil
}

type PassThroughProcessor struct{}

func (PassThroughProcessor) Process(w http.ResponseWriter, _ *http.Request, result ImageResult) {
	upstream.CopyResponseHeaders(w.Header(), result.Response.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(result.Response.StatusCode)
	_, _ = w.Write(result.RawResponse)
}
