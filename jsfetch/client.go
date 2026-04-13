package main

import (
	"net/http"
	"strings"
	"time"
)

// newHTTPClient creates a standard http.Client with the given timeout.
// TLS verification always uses the default (secure) settings.
func newHTTPClient(timeoutSecs int) *http.Client {
	return &http.Client{
		Timeout: time.Duration(timeoutSecs) * time.Second,
	}
}

// parseHeaders converts a slice of "Key: Value" strings into a map.
func parseHeaders(raw []string) map[string]string {
	out := make(map[string]string, len(raw))
	for _, h := range raw {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}

// requestBuilder returns a function that builds GET requests with a fixed
// User-Agent and custom headers baked in.
func requestBuilder(userAgent string, headers map[string]string) func(string) (*http.Request, error) {
	return func(u string) (*http.Request, error) {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
}

// fetchWithRetry attempts to GET the URL up to (1 + maxRetries) times,
// backing off 500 ms × attempt between tries.
func fetchWithRetry(
	client *http.Client,
	buildReq func(string) (*http.Request, error),
	u string,
	maxRetries int,
	log func(string, ...any),
) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*500) * time.Millisecond
			time.Sleep(backoff)
			log("    [~] Retry %d/%d for %s", attempt, maxRetries, u)
		}
		req, err := buildReq(u)
		if err != nil {
			return nil, err // bad URL, no point retrying
		}
		resp, err := client.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
