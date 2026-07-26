package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Bootstrap struct {
	NewAPIBaseURL       *url.URL
	DataListenAddr      string
	AdminListenAddr     string
	DataAllowedClients  []netip.Prefix
	AdminAllowedClients []netip.Prefix
	PublicImageBase     *url.URL
	DataDir             string
	DatabasePath        string
	MaxJSONBodyBytes    int64
	MaxResponseBytes    int64
	MaxImageBytes       int64
	UpstreamTimeout     time.Duration
	ImageTimeout        time.Duration
	DownloadWorkers     int
	MinFreeBytes        uint64
	AdminUsername       string
	AdminPassword       string
	CookieSecure        bool
}

func LoadBootstrap() (Bootstrap, error) {
	var c Bootstrap
	var err error
	dataPort, err := envPort("DATA_PORT", 15880)
	if err != nil {
		return c, err
	}
	adminPort, err := envPort("ADMIN_PORT", 15881)
	if err != nil {
		return c, err
	}
	if dataPort == adminPort {
		return c, errors.New("DATA_PORT and ADMIN_PORT must differ")
	}
	if c.NewAPIBaseURL, err = parseHTTPURL(env("NEWAPI_BASE_URL", "http://newapi:3000"), "NEWAPI_BASE_URL"); err != nil {
		return c, err
	}
	if c.PublicImageBase, err = parseHTTPURL(env("PUBLIC_IMAGE_BASE_URL", "http://10.0.0.1:"+dataPort+"/_gateway/images/"), "PUBLIC_IMAGE_BASE_URL"); err != nil {
		return c, err
	}
	c.DataListenAddr = ":" + dataPort
	c.AdminListenAddr = ":" + adminPort
	if c.DataAllowedClients, err = envAllowedClients("DATA_ALLOWED_CLIENTS"); err != nil {
		return c, err
	}
	if c.AdminAllowedClients, err = envAllowedClients("ADMIN_ALLOWED_CLIENTS"); err != nil {
		return c, err
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

func envAllowedClients(key string) ([]netip.Prefix, error) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	seen := map[netip.Prefix]struct{}{
		prefixes[0]: {},
		prefixes[1]: {},
	}
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return prefixes, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("%s contains an empty entry", key)
		}
		prefix, err := parseAllowedClient(entry)
		if err != nil {
			return nil, fmt.Errorf("%s entry %q: %w", key, entry, err)
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseAllowedClient(raw string) (netip.Prefix, error) {
	if strings.Contains(raw, "/") {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return netip.Prefix{}, errors.New("must be an IP address or CIDR prefix")
		}
		if prefix.Addr().Is4In6() {
			bits := prefix.Bits() - 96
			if bits < 0 {
				return netip.Prefix{}, errors.New("IPv4-mapped CIDR prefix is too broad")
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), bits)
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" {
		return netip.Prefix{}, errors.New("must be an IP address or CIDR prefix without a zone")
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
func envInt64(key string, fallback, min int64) (int64, error) {
	raw := env(key, strconv.FormatInt(fallback, 10))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < min {
		return 0, fmt.Errorf("%s must be an integer >= %d", key, min)
	}
	return n, nil
}

func envPort(key string, fallback int) (string, error) {
	raw := env(key, strconv.Itoa(fallback))
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("%s must be an integer between 1 and 65535", key)
	}
	return strconv.Itoa(port), nil
}
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(env(key, fallback.String()))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return d, nil
}
