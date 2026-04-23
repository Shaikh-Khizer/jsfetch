package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// newHTTPClient creates an HTTP client with:
// - optional proxy
// - optional TLS verification bypass (-k)
// - optional keep-alive disabling (for debugging / proxy visibility)
func newHTTPClient(timeoutSecs int, proxyURL *url.URL, insecure bool) *http.Client {
	transport := &http.Transport{
		// 🔥 Disable HTTP/2 (important for Burp/ZAP visibility)
		ForceAttemptHTTP2: false,

		// 🔥 TLS config (supports -k)
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
		},

		// 🔥 Disable connection reuse so every request hits proxy visibly
		DisableKeepAlives: true,
	}

	// 🔥 Force proxy for ALL requests (page + JS + redirects)
	if proxyURL != nil {
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			return proxyURL, nil
		}
	}

	return &http.Client{
		Timeout:   time.Duration(timeoutSecs) * time.Second,
		Transport: transport,
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

// requestBuilder builds HTTP requests with:
// - custom method
// - user-agent
// - additional headers
func requestBuilder(method, userAgent string, headers map[string]string) func(string) (*http.Request, error) {
	return func(u string) (*http.Request, error) {
		req, err := http.NewRequest(method, u, nil)
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

// fetchWithRetry performs request with retry logic.
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
			return nil, err
		}

		resp, err := client.Do(req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
	}

	return nil, lastErr
}