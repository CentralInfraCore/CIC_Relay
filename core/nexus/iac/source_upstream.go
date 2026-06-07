package iac

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	defaultUpstreamTimeout = 10 * time.Second
	maxRetries             = 3
)

var retryBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// UpstreamSource implements IaCSource by fetching config from an upstream HTTP endpoint.
// The endpoint must return a JSON or YAML object mapping relative file paths to content strings.
// On error it logs WARN and propagates a wrapped error (degraded mode — non-fatal to callers).
type UpstreamSource struct {
	url    string
	token  string
	client *http.Client
	logger *logrus.Logger
}

// NewUpstreamSource creates an UpstreamSource with the given URL, bearer token, timeout and logger.
// token may be empty (no auth). timeout=0 falls back to the 10s default.
func NewUpstreamSource(url, token string, timeout time.Duration, logger *logrus.Logger) *UpstreamSource {
	if timeout <= 0 {
		timeout = defaultUpstreamTimeout
	}
	return &UpstreamSource{
		url:    url,
		token:  token,
		client: &http.Client{Timeout: timeout},
		logger: logger,
	}
}

// NewUpstreamSourceFromEnv reads CIC_UPSTREAM_URL, CIC_UPSTREAM_TOKEN, and
// CIC_UPSTREAM_FETCH_TIMEOUT from the environment and returns a configured UpstreamSource.
// Returns (nil, nil) when CIC_UPSTREAM_URL is absent — caller should fall back to FileSource.
func NewUpstreamSourceFromEnv(logger *logrus.Logger) (*UpstreamSource, error) {
	rawURL := os.Getenv("CIC_UPSTREAM_URL")
	if rawURL == "" {
		return nil, nil
	}

	timeout := defaultUpstreamTimeout
	if val := os.Getenv("CIC_UPSTREAM_FETCH_TIMEOUT"); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return nil, fmt.Errorf("invalid CIC_UPSTREAM_FETCH_TIMEOUT %q: %w", val, err)
		}
		timeout = d
	}

	return NewUpstreamSource(rawURL, os.Getenv("CIC_UPSTREAM_TOKEN"), timeout, logger), nil
}

// FetchConfig performs an HTTP GET with retry/backoff and returns the parsed config map.
// The upstream response must be a flat JSON or YAML object: {"path/to/file.yaml": "<content>"}.
func (s *UpstreamSource) FetchConfig(ctx context.Context) (map[string][]byte, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryBackoff[attempt-1]
			s.logger.WithFields(logrus.Fields{
				"attempt": attempt + 1,
				"backoff": delay,
				"url":     s.url,
			}).Warn("upstream fetch failed, retrying")
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("upstream fetch cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		result, err := s.fetchOnce(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	s.logger.WithError(lastErr).WithField("url", s.url).Warn("upstream fetch failed after all retries")
	return nil, fmt.Errorf("upstream fetch %s failed after %d attempts: %w", s.url, maxRetries, lastErr)
}

// fetchOnce performs a single HTTP GET and parses the response.
func (s *UpstreamSource) fetchOnce(ctx context.Context) (map[string][]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	return parseUpstreamResponse(resp.Header.Get("Content-Type"), body)
}

// parseUpstreamResponse detects JSON vs YAML from Content-Type and unmarshals
// the body as map[string]string, returning map[string][]byte.
func parseUpstreamResponse(contentType string, body []byte) (map[string][]byte, error) {
	ct := strings.ToLower(contentType)
	var raw map[string]string

	switch {
	case strings.Contains(ct, "yaml"):
		if err := yaml.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("yaml unmarshal upstream response: %w", err)
		}
	default:
		// Default to JSON (application/json or unset Content-Type).
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("json unmarshal upstream response: %w", err)
		}
	}

	result := make(map[string][]byte, len(raw))
	for k, v := range raw {
		result[k] = []byte(v)
	}
	return result, nil
}
