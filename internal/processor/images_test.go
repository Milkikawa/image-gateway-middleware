package processor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"image-gateway-middleware/internal/audit"
	"image-gateway-middleware/internal/httpdata"
	"image-gateway-middleware/internal/image"
	"image-gateway-middleware/internal/persistence"
	"image-gateway-middleware/internal/requestbody"
)

func TestProcessRewritesAndPersists(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0}
	imgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer imgServer.Close()
	root := t.TempDir()
	layout, err := prepare(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(context.Background(), filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base, _ := url.Parse("http://10.0.0.1:8080/_gateway/images/")
	d := image.NewDownloader(image.Storage{Images: layout[0], Temp: layout[1]}, time.Second, 3, 0, 1024, 2, 2)
	d.UseHTTPClient(imgServer.Client(), 2)
	d.SetURLValidator(func(*url.URL) error { return nil })
	p := New(d, audit.New(store.DB), base, store.DB)
	raw := []byte(`{"data":[{"url":"` + imgServer.URL + `"}]}`)
	rec := httptest.NewRecorder()
	p.Process(rec, httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader("")), httpdata.ImageResult{Audit: requestbody.Audit{Raw: []byte(`{"model":"gpt-image-2"}`), Model: "gpt-image-2", Fields: map[string][]string{}}, RawResponse: raw, Response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}}, Started: time.Now()})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/_gateway/images/") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var requests, images int
	_ = store.DB.QueryRow(`SELECT count(*) FROM requests`).Scan(&requests)
	_ = store.DB.QueryRow(`SELECT count(*) FROM images WHERE status='READY'`).Scan(&images)
	if requests != 1 || images != 1 {
		t.Fatalf("requests=%d images=%d", requests, images)
	}
}
func TestPreflightBlocksBeforeUpstreamHook(t *testing.T) {
	old := freeBytes
	defer func() { freeBytes = old }()
	freeBytes = func(string) (uint64, error) { return 1, nil }
	p := &Images{}
	if p.Preflight(context.Background(), 2, "/") == nil {
		t.Fatal("expected insufficient storage")
	}
}
func prepare(root string) ([2]string, error) {
	var out [2]string
	out[0] = filepath.Join(root, "images")
	out[1] = filepath.Join(root, "tmp")
	if err := osMkdir(out[0]); err != nil {
		return out, err
	}
	if err := osMkdir(out[1]); err != nil {
		return out, err
	}
	return out, nil
}

var osMkdir = func(path string) error { return mkdirAll(path) }
var mkdirAll = func(path string) error { return os.MkdirAll(path, 0755) }
