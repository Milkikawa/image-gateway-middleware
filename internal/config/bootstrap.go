package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Bootstrap struct {
	NewAPIBaseURL    *url.URL
	DataListenAddr   string
	AdminListenAddr  string
	PublicImageBase  *url.URL
	DataDir          string
	DatabasePath     string
	MaxJSONBodyBytes int64
	MaxResponseBytes int64
	MaxImageBytes    int64
	UpstreamTimeout  time.Duration
	ImageTimeout     time.Duration
	DownloadWorkers  int
	MinFreeBytes     uint64
	AdminUsername    string
	AdminPassword    string
	CookieSecure     bool
}

func LoadBootstrap() (Bootstrap, error) {
	var c Bootstrap
	var err error
	if c.NewAPIBaseURL, err = parseHTTPURL(env("NEWAPI_BASE_URL", "http://newapi:3000"), "NEWAPI_BASE_URL"); err != nil {
		return c, err
	}
	if c.PublicImageBase, err = parseHTTPURL(env("PUBLIC_IMAGE_BASE_URL", "http://10.0.0.1:8080/_gateway/images/"), "PUBLIC_IMAGE_BASE_URL"); err != nil {
		return c, err
	}
	c.DataListenAddr = env("DATA_LISTEN_ADDR", ":8080")
	c.AdminListenAddr = env("ADMIN_LISTEN_ADDR", ":8081")
	if c.DataListenAddr == c.AdminListenAddr {
		return c, errors.New("DATA_LISTEN_ADDR and ADMIN_LISTEN_ADDR must differ")
	}
	c.DataDir = filepath.Clean(env("DATA_DIR", "/data"))
	if !filepath.IsAbs(c.DataDir) {
		return c, errors.New("DATA_DIR must be absolute")
	}
	c.DatabasePath = filepath.Join(c.DataDir, "database", "gateway.db")
	if c.MaxJSONBodyBytes, err = envInt64("MAX_JSON_BODY_BYTES", 4<<20, 1024); err != nil {
		return c, err
	}
	if c.MaxResponseBytes, err = envInt64("MAX_RESPONSE_BYTES", 16<<20, 1024); err != nil {
		return c, err
	}
	if c.MaxImageBytes, err = envInt64("MAX_IMAGE_BYTES", 64<<20, 1024); err != nil {
		return c, err
	}
	if c.UpstreamTimeout, err = envDuration("UPSTREAM_TIMEOUT", 10*time.Minute); err != nil {
		return c, err
	}
	if c.ImageTimeout, err = envDuration("IMAGE_TIMEOUT", 2*time.Minute); err != nil {
		return c, err
	}
	workers, err := envInt64("DOWNLOAD_WORKERS", 4, 1)
	if err != nil {
		return c, err
	}
	c.DownloadWorkers = int(workers)
	free, err := envInt64("MIN_FREE_BYTES", 2<<30, 0)
	if err != nil {
		return c, err
	}
	c.MinFreeBytes = uint64(free)
	c.AdminUsername = env("ADMIN_USERNAME", "admin")
	c.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	if len(c.AdminPassword) < 12 {
		return c, errors.New("ADMIN_PASSWORD must contain at least 12 characters")
	}
	c.CookieSecure, err = strconv.ParseBool(env("COOKIE_SECURE", "false"))
	if err != nil {
		return c, fmt.Errorf("COOKIE_SECURE: %w", err)
	}
	return c, nil
}

func parseHTTPURL(raw, name string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("%s must be an absolute http(s) URL without userinfo", name)
	}
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}
func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
func envInt64(key string, fallback, min int64) (int64, error) {
	raw := env(key, strconv.FormatInt(fallback, 10))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < min {
		return 0, fmt.Errorf("%s must be an integer >= %d", key, min)
	}
	return n, nil
}
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(env(key, fallback.String()))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return d, nil
}
