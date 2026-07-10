package processor

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"image-gateway-middleware/internal/audit"
	"image-gateway-middleware/internal/httpdata"
	"image-gateway-middleware/internal/image"
	imgresponse "image-gateway-middleware/internal/response"
	"image-gateway-middleware/internal/upstream"
)

const PlaceholderID = "download-failed"

type Images struct {
	downloader *image.Downloader
	audit      *audit.Service
	publicBase *url.URL
	db         *sql.DB
}

func New(d *image.Downloader, a *audit.Service, base *url.URL, db *sql.DB) *Images {
	return &Images{downloader: d, audit: a, publicBase: base, db: db}
}
func (p *Images) Process(w http.ResponseWriter, r *http.Request, result httpdata.ImageResult) {
	requestID, err := image.RandomID()
	if err != nil {
		http.Error(w, "request id failure", 500)
		return
	}
	rewritten := result.RawResponse
	status := "READY"
	var records []audit.Image
	if result.Response.StatusCode >= 200 && result.Response.StatusCode < 300 && strings.Contains(result.Response.Header.Get("Content-Type"), "json") {
		doc, parseErr := imgresponse.Parse(result.RawResponse)
		if parseErr == nil {
			urls := doc.URLs()
			mapping := map[string]string{}
			unique := map[string]bool{}
			var mu sync.Mutex
			var wg sync.WaitGroup
			for _, raw := range urls {
				if unique[raw] {
					continue
				}
				unique[raw] = true
				wg.Add(1)
				go func(source string) {
					defer wg.Done()
					download := p.downloader.Download(r.Context(), source)
					record := audit.Image{ID: download.Stored.ID, SourceURL: source, LocalPath: download.Stored.Path, MIME: download.Stored.MIME, Size: download.Stored.Size, SHA256: download.Stored.SHA256, Attempts: download.Attempts}
					if download.Error != "" {
						failedID, idErr := image.RandomID()
						if idErr != nil {
							failedID = requestID + "-failed"
						}
						record.ID = failedID
						record.Status = "FAILED"
						record.Error = download.Error
						record.PublicURL = p.publicURL(record.ID)
						mu.Lock()
						status = "DEGRADED"
					} else {
						record.Status = "READY"
						record.PublicURL = p.publicURL(download.Stored.ID)
						mu.Lock()
					}
					mapping[source] = record.PublicURL
					records = append(records, record)
					mu.Unlock()
				}(raw)
			}
			wg.Wait()
			if len(mapping) > 0 {
				if out, e := doc.Rewrite(mapping); e == nil {
					rewritten = out
				} else {
					status = "FAILED"
				}
			}
		} else {
			status = "PARSE_FAILED"
		}
	}
	reqBody := result.Audit.Raw
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/") {
		reqBody = nil
	}
	saveErr := p.audit.Save(r.Context(), audit.Request{ID: requestID, Method: r.Method, Path: r.URL.Path, Model: result.Audit.Model, Prompt: result.Audit.Prompt, RequestBody: reqBody, RawResponse: result.RawResponse, RewrittenResponse: rewritten, UpstreamStatus: result.Response.StatusCode, DownstreamStatus: result.Response.StatusCode, Status: status, DurationMS: time.Since(result.Started).Milliseconds(), Fields: result.Audit.Fields, Files: result.Audit.Files, Images: records})
	if saveErr != nil {
		audit.CleanupFiles(records)
		http.Error(w, "failed to persist gateway result", http.StatusInternalServerError)
		return
	}
	upstream.CopyResponseHeaders(w.Header(), result.Response.Header)
	for _, name := range []string{"Content-Length", "Content-Encoding", "ETag", "Digest", "Content-MD5"} {
		w.Header().Del(name)
	}
	w.Header().Set("X-Image-Gateway-Request-Id", requestID)
	w.Header().Set("X-Image-Gateway-Status", strings.ToLower(status))
	w.WriteHeader(result.Response.StatusCode)
	_, _ = w.Write(rewritten)
}
func (p *Images) publicURL(id string) string {
	u := *p.publicBase
	u.Path = strings.TrimRight(p.publicBase.Path, "/") + "/" + id
	return u.String()
}
func (p *Images) ServeImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/_gateway/images/")
	if id == PlaceholderID {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(placeholder)
		return
	}
	var path, mimeType string
	var size int64
	var status string
	err := p.db.QueryRowContext(r.Context(), `SELECT local_path,mime,size,status FROM images WHERE id=?`, id).Scan(&path, &mimeType, &size, &status)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if status == "FAILED" {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(placeholder)
		return
	}
	if status != "READY" {
		http.NotFound(w, r)
		return
	}
	file, err := http.Dir(filepath.Dir(path)).Open(filepath.Base(path))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != size {
		http.Error(w, "stored image unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}
func (p *Images) Preflight(ctx context.Context, min uint64, root string) error {
	free, err := freeBytes(root)
	if err != nil {
		return err
	}
	if free < min {
		return fmt.Errorf("insufficient free storage")
	}
	return nil
}

var freeBytes = func(path string) (uint64, error) { return storageFree(path) }
var placeholder = []byte("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1024\" height=\"1024\"><rect width=\"100%\" height=\"100%\" fill=\"#20242b\"/><text x=\"50%\" y=\"48%\" fill=\"#fff\" font-size=\"58\" text-anchor=\"middle\">图片下载失败</text><text x=\"50%\" y=\"56%\" fill=\"#aaa\" font-size=\"32\" text-anchor=\"middle\">请在网关控制台查看详情</text></svg>")
