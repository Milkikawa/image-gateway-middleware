package admin

import (
	"database/sql"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"image-gateway-middleware/internal/config"
	"image-gateway-middleware/internal/image"
	"image-gateway-middleware/internal/storage"
)

type Server struct {
	db         *sql.DB
	auth       *Auth
	downloader *image.Downloader
	base       *url.URL
	dataDir    string
	templates  *template.Template
	onRuntime  func(config.Runtime)
	logger     *slog.Logger
}
type pageData struct {
	Title        string
	CSRF         string
	Requests     []requestRow
	Images       []imageRow
	Request      *requestDetail
	Runtime      config.Runtime
	Free         uint64
	Error        string
	Page         int
	PreviousPage int
	NextPage     int
	HasPrevious  bool
	HasNext      bool
}
type requestRow struct {
	ID, CreatedAt, Model, Prompt, Status string
	UpstreamStatus                       int
	DurationMS                           int64
}
type imageRow struct {
	ID, CreatedAt, SourceURL, PublicURL, Status, Error, MIME string
	Size                                                     int64
}
type requestDetail struct {
	requestRow
	RequestBody, RawResponse, RewrittenResponse string
}

func NewServer(db *sql.DB, auth *Auth, downloader *image.Downloader, base *url.URL, dataDir string, onRuntime ...func(config.Runtime)) *Server {
	s := &Server{db: db, auth: auth, downloader: downloader, base: base, dataDir: dataDir, templates: template.Must(template.New("pages").Parse(pageTemplates))}
	if len(onRuntime) > 0 {
		s.onRuntime = onRuntime[0]
	}
	return s
}

// SetLogger enables structured diagnostics. A nil logger disables them.
func (s *Server) SetLogger(logger *slog.Logger) { s.logger = logger }

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serve) }
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: http: https:; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.URL.Path == "/login" {
		s.login(w, r)
		return
	}
	if !s.auth.Authenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost && !s.auth.ValidCSRF(r) {
		if s.logger != nil {
			s.logger.Warn("admin CSRF validation failed",
				"component", "csrf",
				"plane", "admin",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"reason", "invalid_token",
			)
		}
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.dashboard(w, r)
	case r.URL.Path == "/logout" && r.Method == http.MethodPost:
		s.auth.Logout(w, r)
		http.Redirect(w, r, "/login", 303)
	case r.URL.Path == "/requests" && r.Method == http.MethodGet:
		s.requests(w, r)
	case strings.HasPrefix(r.URL.Path, "/requests/") && strings.HasSuffix(r.URL.Path, "/delete") && r.Method == http.MethodPost:
		s.deleteRequest(w, r)
	case strings.HasPrefix(r.URL.Path, "/requests/") && r.Method == http.MethodGet:
		s.requestDetail(w, r)
	case r.URL.Path == "/images" && r.Method == http.MethodGet:
		s.images(w, r)
	case strings.HasPrefix(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/delete") && r.Method == http.MethodPost:
		s.deleteImage(w, r)
	case strings.HasPrefix(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/retry") && r.Method == http.MethodPost:
		s.retryImage(w, r)
	case r.URL.Path == "/settings" && r.Method == http.MethodGet:
		s.settings(w, r)
	case r.URL.Path == "/settings" && r.Method == http.MethodPost:
		s.updateSettings(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, "login", pageData{Title: "登录"})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	if err := s.auth.Login(w, r, r.FormValue("username"), r.FormValue("password")); err != nil {
		s.render(w, "login", pageData{Title: "登录", Error: "登录失败"})
		return
	}
	http.Redirect(w, r, "/", 303)
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	var reqs, imgs int64
	_ = s.db.QueryRowContext(r.Context(), `SELECT count(*) FROM requests`).Scan(&reqs)
	_ = s.db.QueryRowContext(r.Context(), `SELECT count(*) FROM images`).Scan(&imgs)
	free, _ := storage.FreeBytes(s.dataDir)
	s.render(w, "dashboard", s.data(r, pageData{Title: "概览", Free: free, Error: fmt.Sprintf("请求 %d · 图片 %d", reqs, imgs)}))
}
func (s *Server) requests(w http.ResponseWriter, r *http.Request) {
	pageNumber := page(r)
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,created_at,model,prompt,status,upstream_status,duration_ms FROM requests ORDER BY created_at DESC LIMIT ? OFFSET ?`, pageSize+1, (pageNumber-1)*pageSize)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	d := s.data(r, pageData{Title: "请求", Page: pageNumber, PreviousPage: pageNumber - 1, NextPage: pageNumber + 1, HasPrevious: pageNumber > 1})
	for rows.Next() {
		var x requestRow
		if rows.Scan(&x.ID, &x.CreatedAt, &x.Model, &x.Prompt, &x.Status, &x.UpstreamStatus, &x.DurationMS) == nil {
			d.Requests = append(d.Requests, x)
		}
	}
	if len(d.Requests) > pageSize {
		d.HasNext = true
		d.Requests = d.Requests[:pageSize]
	}
	s.render(w, "requests", d)
}
func (s *Server) requestDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/requests/")
	var d requestDetail
	d.ID = id
	var reqBody, raw, rewritten []byte
	err := s.db.QueryRowContext(r.Context(), `SELECT created_at,model,prompt,status,upstream_status,duration_ms,request_body,raw_response,rewritten_response FROM requests WHERE id=?`, id).Scan(&d.CreatedAt, &d.Model, &d.Prompt, &d.Status, &d.UpstreamStatus, &d.DurationMS, &reqBody, &raw, &rewritten)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d.RequestBody = string(reqBody)
	d.RawResponse = string(raw)
	d.RewrittenResponse = string(rewritten)
	s.render(w, "request", s.data(r, pageData{Title: "请求详情", Request: &d}))
}
func (s *Server) images(w http.ResponseWriter, r *http.Request) {
	pageNumber := page(r)
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,created_at,source_url,public_url,status,error,mime,size FROM images ORDER BY created_at DESC LIMIT ? OFFSET ?`, pageSize+1, (pageNumber-1)*pageSize)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	d := s.data(r, pageData{Title: "图片", Page: pageNumber, PreviousPage: pageNumber - 1, NextPage: pageNumber + 1, HasPrevious: pageNumber > 1})
	for rows.Next() {
		var x imageRow
		if rows.Scan(&x.ID, &x.CreatedAt, &x.SourceURL, &x.PublicURL, &x.Status, &x.Error, &x.MIME, &x.Size) == nil {
			d.Images = append(d.Images, x)
		}
	}
	if len(d.Images) > pageSize {
		d.HasNext = true
		d.Images = d.Images[:pageSize]
	}
	s.render(w, "images", d)
}
func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/requests/"), "/delete")
	rows, err := s.db.QueryContext(r.Context(), `SELECT local_path FROM images WHERE request_id=? AND local_path<>''`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var paths []string
	for rows.Next() {
		var path string
		if rows.Scan(&path) == nil {
			paths = append(paths, path)
		}
	}
	_ = rows.Close()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM requests WHERE id=?`, id)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, path := range paths {
		_ = os.Remove(path)
	}
	http.Redirect(w, r, "/requests", 303)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/images/"), "/delete")
	var path string
	if err := s.db.QueryRowContext(r.Context(), `SELECT local_path FROM images WHERE id=?`, id).Scan(&path); err != nil {
		http.NotFound(w, r)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM images WHERE id=?`, id)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if path != "" {
		_ = os.Remove(path)
	}
	http.Redirect(w, r, "/images", 303)
}
func (s *Server) retryImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/images/"), "/retry")
	var source string
	if err := s.db.QueryRowContext(r.Context(), `SELECT source_url FROM images WHERE id=? AND status='FAILED'`, id).Scan(&source); err != nil {
		http.NotFound(w, r)
		return
	}
	result := s.downloader.Download(r.Context(), source)
	if result.Error != "" {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE images SET error=?,attempt_count=? WHERE id=?`, result.Error, len(result.Attempts), id)
		http.Redirect(w, r, "/images", 303)
		return
	}
	public := s.publicURL(id)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE images SET local_path=?,public_url=?,mime=?,size=?,sha256=?,status='READY',error='',attempt_count=? WHERE id=?`, result.Stored.Path, public, result.Stored.MIME, result.Stored.Size, result.Stored.SHA256, len(result.Attempts), id)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		_ = image.RemoveStored(result.Stored)
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/images", 303)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	runtime, err := config.LoadRuntime(r.Context(), s.db)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "settings", s.data(r, pageData{Title: "设置", Runtime: runtime}))
}
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{"download_attempts": r.FormValue("download_attempts"), "retry_base_delay": r.FormValue("retry_base_delay"), "max_redirects": r.FormValue("max_redirects")}
	runtime, err := config.UpdateRuntime(r.Context(), s.db, values)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if s.onRuntime != nil {
		s.onRuntime(runtime)
	}
	http.Redirect(w, r, "/settings", 303)
}
func (s *Server) data(r *http.Request, d pageData) pageData {
	if c, e := r.Cookie(csrfCookie); e == nil {
		d.CSRF = c.Value
	}
	return d
}
func (s *Server) render(w http.ResponseWriter, name string, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, d); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
func (s *Server) publicURL(id string) string {
	u := *s.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + id
	return u.String()
}

const pageSize = 50

func page(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n < 1 {
		return 1
	}
	return n
}
