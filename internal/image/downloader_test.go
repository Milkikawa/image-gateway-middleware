package image

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"image-gateway-middleware/internal/observability"
)

var png = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}

func TestDownloadRetriesAndAtomicSave(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			http.Error(w, "retry", 500)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer s.Close()
	root := t.TempDir()
	images := root + "/images"
	tmp := root + "/tmp"
	_ = os.MkdirAll(images, 0755)
	_ = os.MkdirAll(tmp, 0755)
	d := NewDownloader(Storage{Images: images, Temp: tmp}, time.Second, 3, time.Millisecond, 1024, 1, 2)
	d.UseHTTPClient(s.Client(), 2)
	d.SetURLValidator(func(*url.URL) error { return nil })
	var output bytes.Buffer
	d.SetLogger(observability.NewLogger(&output, slog.LevelInfo))
	result := d.Download(context.Background(), s.URL+"/private/path?token=super-secret")
	if result.Error != "" || calls.Load() != 3 || result.Stored.Size != int64(len(png)) {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
	got, _ := os.ReadFile(result.Stored.Path)
	if !bytes.Equal(got, png) {
		t.Fatal("saved bytes differ")
	}
	logged := output.String()
	for _, expected := range []string{`"component":"image_download"`, `"target_scheme":"http"`, `"reason":"http_status"`, `"status":500`, `"retry":true`} {
		if !strings.Contains(logged, expected) {
			t.Errorf("log missing %s: %s", expected, logged)
		}
	}
	for _, secret := range []string{"/private/path", "token=", "super-secret", s.URL} {
		if strings.Contains(logged, secret) {
			t.Errorf("log leaked %q: %s", secret, logged)
		}
	}
}
func TestPrivateLiteralIPRejected(t *testing.T) {
	d := NewDownloader(Storage{}, time.Second, 3, 0, 1024, 1, 2)
	var output bytes.Buffer
	d.SetLogger(observability.NewLogger(&output, slog.LevelInfo))
	r := d.Download(context.Background(), "http://user:password@127.0.0.1/private/path?token=super-secret")
	if r.Error == "" || len(r.Attempts) != 0 {
		t.Fatalf("result=%+v", r)
	}
	logged := output.String()
	for _, expected := range []string{`"component":"image_download"`, `"target_scheme":"http"`, `"target_host":"127.0.0.1"`, `"reason":"url_rejected"`} {
		if !strings.Contains(logged, expected) {
			t.Errorf("log missing %s: %s", expected, logged)
		}
	}
	for _, secret := range []string{"user", "password", "/private/path", "token", "super-secret"} {
		if strings.Contains(logged, secret) {
			t.Errorf("log leaked %q: %s", secret, logged)
		}
	}
}
