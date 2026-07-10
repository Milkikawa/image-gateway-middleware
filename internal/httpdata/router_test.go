package httpdata

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"image-gateway-middleware/internal/upstream"
)

type captureProcessor struct{ result chan ImageResult }

func (p captureProcessor) Process(w http.ResponseWriter, _ *http.Request, r ImageResult) {
	p.result <- r
	w.WriteHeader(r.Response.StatusCode)
	_, _ = w.Write(r.RawResponse)
}

func TestStrictRoutesAndSingleUpstreamRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	results := make(chan ImageResult, 1)
	p := NewProxy(upstream.New(base, time.Second), 1024, 4096, captureProcessor{results})
	router := NewRouter(p, http.NotFoundHandler(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations?x=1", strings.NewReader(`{"model":"gpt-image-2","prompt":"cat"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 || calls.Load() != 1 {
		t.Fatalf("code=%d calls=%d", rec.Code, calls.Load())
	}
	r := <-results
	if r.Audit.Model != "gpt-image-2" {
		t.Fatalf("model=%q", r.Audit.Model)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/v1/images/generations", 405}, {"POST", "/v1/models", 405}, {"POST", "/v1/chat/completions", 404}} {
		before := calls.Load()
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != tc.want {
			t.Errorf("%s %s=%d", tc.method, tc.path, rr.Code)
		}
		if calls.Load() != before {
			t.Fatal("rejected route reached upstream")
		}
	}
}

func TestMultipartBodyIsByteIdenticalAndAudited(t *testing.T) {
	var original bytes.Buffer
	mw := multipart.NewWriter(&original)
	_ = mw.WriteField("model", "gpt-image-2")
	_ = mw.WriteField("prompt", "edit cat")
	fh, _ := mw.CreateFormFile("image", "input.png")
	_, _ = fh.Write([]byte{0, 1, 2, 3, 4, 255})
	_ = mw.Close()
	expected := append([]byte(nil), original.Bytes()...)
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	results := make(chan ImageResult, 1)
	p := NewProxy(upstream.New(base, time.Second), 1024, 4096, captureProcessor{results})
	router := NewRouter(p, http.NotFoundHandler(), http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(expected))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(expected, received) {
		t.Fatal("upstream multipart bytes changed")
	}
	r := <-results
	if r.Audit.Model != "gpt-image-2" || len(r.Audit.Files) != 1 || r.Audit.Files[0].Size != 6 {
		t.Fatalf("audit=%+v", r.Audit)
	}
}
