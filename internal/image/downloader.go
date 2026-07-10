package image

import (
	"context"
	"fmt"
	"io"
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
		return Result{Error: err.Error()}
	}
	if err = policy.validate(u); err != nil {
		return Result{Error: err.Error()}
	}

	select {
	case d.semaphore <- struct{}{}:
		defer func() { <-d.semaphore }()
	case <-ctx.Done():
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
		stored, status, attemptErr, retry := d.once(ctx, &clientCopy, u)
		attempt := Attempt{Number: n, HTTPStatus: status, DurationMS: time.Since(started).Milliseconds()}
		if attemptErr == nil {
			result.Stored = stored
			result.Attempts = append(result.Attempts, attempt)
			result.Error = ""
			return result
		}
		attempt.Error = attemptErr.Error()
		result.Attempts = append(result.Attempts, attempt)
		result.Error = attempt.Error
		if !retry || n == policy.attempts {
			break
		}
		timer := time.NewTimer(policy.retryDelay * time.Duration(1<<(n-1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Error = ctx.Err().Error()
			return result
		case <-timer.C:
		}
	}
	return result
}

func (d *Downloader) once(ctx context.Context, client *http.Client, u *url.URL) (Stored, int, error, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Stored{}, 0, err, false
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/webp,image/gif;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return Stored{}, 0, err, true
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
		return Stored{}, resp.StatusCode, fmt.Errorf("image server returned %d", resp.StatusCode), retry
	}
	if resp.ContentLength > d.maxBytes {
		return Stored{}, resp.StatusCode, fmt.Errorf("image exceeds %d bytes", d.maxBytes), false
	}
	id, err := RandomID()
	if err != nil {
		return Stored{}, resp.StatusCode, err, false
	}
	stored, err := d.storage.Save(id, resp.Header.Get("Content-Type"), resp.Body, d.maxBytes)
	return stored, resp.StatusCode, err, false
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
