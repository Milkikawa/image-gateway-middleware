package httpdata_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"image-gateway-middleware/internal/audit"
	"image-gateway-middleware/internal/httpdata"
	"image-gateway-middleware/internal/image"
	"image-gateway-middleware/internal/persistence"
	"image-gateway-middleware/internal/processor"
	"image-gateway-middleware/internal/storage"
	"image-gateway-middleware/internal/upstream"
)

func TestImageGenerationEndToEnd(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 1, 2, 3}
	var imageCalls atomic.Int32
	var imageAuthorization atomic.Value
	imageHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		imageCalls.Add(1)
		imageAuthorization.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer imageHost.Close()

	var upstreamCalls atomic.Int32
	var upstreamAuthorization atomic.Value
	newAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		upstreamAuthorization.Store(r.Header.Get("Authorization"))
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"url":"`+imageHost.URL+`"}]}`)
	}))
	defer newAPI.Close()

	root := t.TempDir()
	layout, err := storage.Prepare(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(context.Background(), filepath.Join(layout.Database, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base, _ := url.Parse("http://gateway.invalid/_gateway/images/")
	d := image.NewDownloader(image.Storage{Images: layout.Images, Temp: layout.Temp}, time.Second, 3, 0, 1<<20, 2, 2)
	d.UseHTTPClient(imageHost.Client(), 2)
	d.SetURLValidator(func(*url.URL) error { return nil })
	p := processor.New(d, audit.New(store.DB), base, store.DB)
	upstreamBase, _ := url.Parse(newAPI.URL)
	proxy := httpdata.NewProxy(upstream.New(upstreamBase, time.Second), 1<<20, 1<<20, p)
	gateway := httptest.NewServer(httpdata.NewRouter(proxy, http.HandlerFunc(p.ServeImage), httpdata.Health(store.DB)))
	defer gateway.Close()

	req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/images/generations", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream calls=%d, want 1", upstreamCalls.Load())
	}
	if upstreamAuthorization.Load() != "Bearer secret" {
		t.Fatalf("authorization not forwarded to newapi")
	}
	if got, _ := imageAuthorization.Load().(string); got != "" {
		t.Fatalf("authorization leaked to image host: %q", got)
	}
	if imageCalls.Load() != 1 {
		t.Fatalf("image calls=%d, want 1", imageCalls.Load())
	}

	var document struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Data) != 1 {
		t.Fatalf("rewritten response=%s", body)
	}
	rewritten, err := url.Parse(document.Data[0].URL)
	if err != nil || !strings.HasPrefix(rewritten.Path, "/_gateway/images/") {
		t.Fatalf("bad rewritten URL %q", document.Data[0].URL)
	}
	storedResp, err := gateway.Client().Get(gateway.URL + rewritten.Path)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(storedResp.Body)
	storedResp.Body.Close()
	if storedResp.StatusCode != http.StatusOK || !bytes.Equal(stored, png) {
		t.Fatalf("stored status=%d body=%x", storedResp.StatusCode, stored)
	}

	var count int
	var raw, rewrittenBody []byte
	if err := store.DB.QueryRow(`SELECT count(*),raw_response,rewritten_response FROM requests`).Scan(&count, &raw, &rewrittenBody); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !bytes.Contains(raw, []byte(imageHost.URL)) || bytes.Contains(rewrittenBody, []byte(imageHost.URL)) {
		t.Fatalf("audit raw=%s rewritten=%s count=%d", raw, rewrittenBody, count)
	}

	models, err := gateway.Client().Get(gateway.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	models.Body.Close()
	if err := store.DB.QueryRow(`SELECT count(*) FROM requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("models wrote audit row: count=%d", count)
	}
	callsBefore := upstreamCalls.Load()
	unknown, err := gateway.Client().Get(gateway.URL + "/v1/unknown")
	if err != nil {
		t.Fatal(err)
	}
	unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound || upstreamCalls.Load() != callsBefore {
		t.Fatalf("unknown status=%d upstream calls changed", unknown.StatusCode)
	}
}

func TestFailedImageRetriesThreeTimesAndServesStablePlaceholder(t *testing.T) {
	var calls atomic.Int32
	imageHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "temporary", http.StatusInternalServerError)
	}))
	defer imageHost.Close()
	root := t.TempDir()
	layout, err := storage.Prepare(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(context.Background(), filepath.Join(layout.Database, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base, _ := url.Parse("http://gateway.invalid/_gateway/images/")
	d := image.NewDownloader(image.Storage{Images: layout.Images, Temp: layout.Temp}, time.Second, 3, 0, 1<<20, 1, 2)
	d.UseHTTPClient(imageHost.Client(), 2)
	d.SetURLValidator(func(*url.URL) error { return nil })
	p := processor.New(d, audit.New(store.DB), base, store.DB)
	raw := []byte(`{"data":[{"url":"` + imageHost.URL + `"}]}`)
	recorder := httptest.NewRecorder()
	p.Process(recorder, httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"x"}`)), httpdata.ImageResult{RawResponse: raw, Response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}}, Started: time.Now()})
	if calls.Load() != 3 {
		t.Fatalf("image calls=%d, want 3", calls.Load())
	}
	if recorder.Header().Get("X-Image-Gateway-Status") != "degraded" {
		t.Fatalf("status header=%q", recorder.Header().Get("X-Image-Gateway-Status"))
	}
	var doc struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(doc.Data[0].URL)
	first := httptest.NewRecorder()
	p.ServeImage(first, httptest.NewRequest(http.MethodGet, u.Path, nil))
	second := httptest.NewRecorder()
	p.ServeImage(second, httptest.NewRequest(http.MethodGet, u.Path, nil))
	if first.Code != 200 || first.Header().Get("Content-Type") != "image/svg+xml" || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("placeholder unstable or unavailable")
	}
}
