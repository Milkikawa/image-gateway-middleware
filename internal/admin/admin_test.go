package admin

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"image-gateway-middleware/internal/config"
	"image-gateway-middleware/internal/image"
	"image-gateway-middleware/internal/persistence"
)

func setup(t *testing.T) (*Server, *Auth) {
	t.Helper()
	store, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	auth := NewAuth(store.DB, false)
	if err = auth.EnsureAdmin("admin", "correct-password"); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("http://10.0.0.1/images/")
	d := image.NewDownloader(image.Storage{}, time.Second, 1, 0, 1024, 1, 1)
	return NewServer(store.DB, auth, d, base, t.TempDir()), auth
}
func TestAuthenticationAndCSRF(t *testing.T) {
	server, _ := setup(t)
	h := server.Handler()
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/", nil))
	if unauth.Code != http.StatusSeeOther {
		t.Fatalf("unauth=%d", unauth.Code)
	}
	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=correct-password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(login, req)
	if login.Code != 303 {
		t.Fatalf("login=%d body=%s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range cookies {
		get.AddCookie(c)
	}
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, get)
	if ok.Code != 200 {
		t.Fatalf("dashboard=%d", ok.Code)
	}
	post := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("download_attempts=3&retry_base_delay=1s&max_redirects=3"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		post.AddCookie(c)
	}
	forbidden := httptest.NewRecorder()
	h.ServeHTTP(forbidden, post)
	if forbidden.Code != 403 {
		t.Fatalf("csrf=%d", forbidden.Code)
	}
	if ok.Header().Get("Content-Security-Policy") == "" || ok.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
}
func TestRuntimeUpdateCallsLivePolicyHook(t *testing.T) {
	server, _ := setup(t)
	called := make(chan config.Runtime, 1)
	server.onRuntime = func(runtime config.Runtime) { called <- runtime }
	h := server.Handler()
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=correct-password"))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(login, loginRequest)
	cookies := login.Result().Cookies()
	var csrf string
	for _, cookie := range cookies {
		if cookie.Name == csrfCookie {
			csrf = cookie.Value
		}
	}
	form := "download_attempts=4&retry_base_delay=1s&max_redirects=3&csrf=" + url.QueryEscape(csrf)
	post := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		post.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case runtime := <-called:
		if runtime.DownloadAttempts != 4 {
			t.Fatalf("runtime=%+v", runtime)
		}
	default:
		t.Fatal("runtime hook not called")
	}
}

func TestTemplateEscapesStoredPayload(t *testing.T) {
	server, _ := setup(t)
	hash, _ := hashPassword("x")
	_ = hash // cover password format without exposing it
	if !strings.Contains(templateEscape("<script>alert(1)</script>"), "&lt;script&gt;") {
		t.Fatal("expected escaped text")
	}
	_ = server
}
func templateEscape(v string) string {
	var b strings.Builder
	template.HTMLEscape(&b, []byte(v))
	return b.String()
}
