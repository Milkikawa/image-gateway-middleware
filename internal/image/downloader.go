package image

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type Attempt struct {
	Number, HTTPStatus int
	Error              string
	DurationMS         int64
}

type Result struct {
	Stored   Stored
	Attempts []Attempt
	Error    string
}

type downloadPolicy struct {
	client       *http.Client
	validate     func(*url.URL) error
	attempts     int
	retryDelay   time.Duration
	maxRedirects int
	logger       *slog.Logger
}

type Downloader struct {
	storage   Storage
	maxBytes  int64
	semaphore chan struct{}

	mu     sync.RWMutex
	policy downloadPolicy
}

func NewDownloader(storage Storage, timeout time.Duration, attempts int, retryDelay time.Duration, maxBytes int64, workers, maxRedirects int) *Downloader {
	return &Downloader{
		storage:   storage,
		maxBytes:  maxBytes,
		semaphore: make(chan struct{}, workers),
		policy: downloadPolicy{
			client:       &http.Client{Timeout: timeout},
			validate:     ValidateRemoteURL,
			attempts:     attempts,
			retryDelay:   retryDelay,
			maxRedirects: maxRedirects,
		},
	}
}

// UseHTTPClient replaces the transport while retaining gateway redirect checks.
// It is primarily useful for an explicit proxy or deterministic tests.
func (d *Downloader) UseHTTPClient(client *http.Client, maxRedirects int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policy.client = client
	d.policy.maxRedirects = maxRedirects
}

// SetURLValidator supports deployment-specific host allowlists and deterministic tests.
func (d *Downloader) SetURLValidator(validate func(*url.URL) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policy.validate = validate
}

// SetLogger enables structured download diagnostics. A nil logger disables them.
func (d *Downloader) SetLogger(logger *slog.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policy.logger = logger
}

// UpdatePolicy atomically changes the policy for downloads that start afterwards.
// A running download keeps the snapshot captured at its start.
func (d *Downloader) UpdatePolicy(attempts int, retryDelay time.Duration, maxRedirects int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policy.attempts = attempts
	d.policy.retryDelay = retryDelay
	d.policy.maxRedirects = maxRedirects
}

func (d *Downloader) snapshot() downloadPolicy {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.policy
}

func (d *Downloader) Download(ctx context.Context, raw string) Result {
	policy := d.snapshot()
	u, err := url.Parse(raw)
	if err != nil {
		logDownload(policy.logger, slog.LevelWarn, "", "", 0, 0, false, "invalid_url", 0)
		return Result{Error: err.Error()}
	}
	scheme, host := u.Scheme, u.Hostname()
	if err = policy.validate(u); err != nil {
		logDownload(policy.logger, slog.LevelWarn, scheme, host, 0, 0, false, "url_rejected", 0)
		return Result{Error: err.Error()}
	}

	select {
	case d.semaphore <- struct{}{}:
		defer func() { <-d.semaphore }()
	case <-ctx.Done():
		logDownload(policy.logger, slog.LevelWarn, scheme, host, 0, 0, false, "context_canceled", 0)
		return Result{Error: ctx.Err().Error()}
	}

	clientCopy := http.Client{
		Transport: policy.client.Transport,
		Jar:       policy.client.Jar,
		Timeout:   policy.client.Timeout,
	}
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > policy.maxRedirects {
			return fmt.Errorf("too many redirects")
		}
		return policy.validate(req.URL)
	}

	var result Result
	for n := 1; n <= policy.attempts; n++ {
		started := time.Now()
		stored, status, attemptErr, retry, reason := d.once(ctx, &clientCopy, u)
		duration := time.Since(started).Milliseconds()
		attempt := Attempt{Number: n, HTTPStatus: status, DurationMS: duration}
		if attemptErr == nil {
			result.Stored = stored
			result.Attempts = append(result.Attempts, attempt)
			result.Error = ""
			logDownload(policy.logger, slog.LevelDebug, scheme, host, n, status, false, "completed", duration)
			return result
		}
		attempt.Error = attemptErr.Error()
		result.Attempts = append(result.Attempts, attempt)
		result.Error = attempt.Error
		willRetry := retry && n < policy.attempts
		logDownload(policy.logger, slog.LevelWarn, scheme, host, n, status, willRetry, reason, duration)
		if !willRetry {
			break
		}
		timer := time.NewTimer(policy.retryDelay * time.Duration(1<<(n-1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Error = ctx.Err().Error()
			logDownload(policy.logger, slog.LevelWarn, scheme, host, n, status, false, "context_canceled", duration)
			return result
		case <-timer.C:
		}
	}
	return result
}

func (d *Downloader) once(ctx context.Context, client *http.Client, u *url.URL) (Stored, int, error, bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Stored{}, 0, err, false, "request_error"
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/webp,image/gif;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return Stored{}, 0, err, true, "network_error"
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
		return Stored{}, resp.StatusCode, fmt.Errorf("image server returned %d", resp.StatusCode), retry, "http_status"
	}
	if resp.ContentLength > d.maxBytes {
		return Stored{}, resp.StatusCode, fmt.Errorf("image exceeds %d bytes", d.maxBytes), false, "response_too_large"
	}
	id, err := RandomID()
	if err != nil {
		return Stored{}, resp.StatusCode, err, false, "storage_error"
	}
	stored, err := d.storage.Save(id, resp.Header.Get("Content-Type"), resp.Body, d.maxBytes)
	if err != nil {
		return Stored{}, resp.StatusCode, err, false, "storage_error"
	}
	return stored, resp.StatusCode, nil, false, "completed"
}

func logDownload(logger *slog.Logger, level slog.Level, scheme, host string, attempt, status int, retry bool, reason string, durationMS int64) {
	if logger == nil {
		return
	}
	attributes := []any{
		"component", "image_download",
		"target_scheme", scheme,
		"target_host", host,
		"attempt", attempt,
		"retry", retry,
		"reason", reason,
	}
	if status != 0 {
		attributes = append(attributes, "status", status)
	}
	if attempt != 0 {
		attributes = append(attributes, "duration_ms", durationMS)
	}
	logger.Log(context.Background(), level, "image download attempt completed", attributes...)
}

func RemoveStored(s Stored) error {
	if s.Path == "" {
		return nil
	}
	err := os.Remove(s.Path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
