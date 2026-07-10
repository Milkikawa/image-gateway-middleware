package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const sessionCookie = "image_gateway_session"
const csrfCookie = "image_gateway_csrf"

type Auth struct {
	db       *sql.DB
	secure   bool
	mu       sync.Mutex
	failures map[string][]time.Time
}

func NewAuth(db *sql.DB, secure bool) *Auth {
	return &Auth{db: db, secure: secure, failures: map[string][]time.Time{}}
}
func (a *Auth) EnsureAdmin(username, password string) error {
	var n int
	if err := a.db.QueryRow(`SELECT count(*) FROM admins`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`INSERT INTO admins(id,username,password_hash,created_at) VALUES(1,?,?,?)`, username, hash, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, username, password string) error {
	if !a.allowed(r.RemoteAddr) {
		return fmt.Errorf("too many login attempts")
	}
	var encoded string
	err := a.db.QueryRow(`SELECT password_hash FROM admins WHERE username=?`, username).Scan(&encoded)
	if err != nil || !verifyPassword(encoded, password) {
		a.failed(r.RemoteAddr)
		return fmt.Errorf("invalid credentials")
	}
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`INSERT INTO sessions(token_hash,csrf_hash,expires_at,created_at) VALUES(?,?,?,?)`, hashToken(token), hashToken(csrf), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secure, MaxAge: 86400})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode, Secure: a.secure, MaxAge: 86400})
	return nil
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashToken(c.Value))
	}
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: name == sessionCookie, SameSite: http.SameSiteStrictMode, Secure: a.secure})
	}
}
func (a *Auth) Authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	var expires string
	if err = a.db.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash=?`, hashToken(c.Value)).Scan(&expires); err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, expires)
	return err == nil && time.Now().Before(t)
}
func (a *Auth) ValidCSRF(r *http.Request) bool {
	session, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	csrf, err := r.Cookie(csrfCookie)
	if err != nil {
		return false
	}
	submitted := r.FormValue("csrf")
	if submitted == "" {
		submitted = r.Header.Get("X-CSRF-Token")
	}
	if subtle.ConstantTimeCompare([]byte(csrf.Value), []byte(submitted)) != 1 {
		return false
	}
	var expected string
	if err = a.db.QueryRow(`SELECT csrf_hash FROM sessions WHERE token_hash=?`, hashToken(session.Value)).Scan(&expected); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(hashToken(submitted))) == 1
}
func (a *Auth) allowed(key string) bool {
	key = remoteHost(key)
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := time.Now().Add(-15 * time.Minute)
	var keep []time.Time
	for _, t := range a.failures[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	a.failures[key] = keep
	return len(keep) < 5
}
func (a *Auth) failed(key string) {
	key = remoteHost(key)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[key] = append(a.failures[key], time.Now())
}
func remoteHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hashToken(v string) string { sum := sha256.Sum256([]byte(v)); return fmt.Sprintf("%x", sum[:]) }
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 2, 32)
	return strings.Join([]string{"argon2id", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)}, "$"), nil
}
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[1])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[2])
	if e1 != nil || e2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 1, 64*1024, 2, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
