package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"time"
)


func newHTTPClient(timeoutSecs int, proxyURL *url.URL, insecure bool, followRedirects bool) *http.Client {
	transport := &http.Transport{
		ForceAttemptHTTP2: false,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
		},
		DisableKeepAlives: true,
		Proxy: nil,
	}
	if proxyURL != nil {
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			return proxyURL, nil
		}
	}

	checkRedirect := func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if followRedirects {
		checkRedirect = nil
	}

	return &http.Client{
		Timeout:       time.Duration(timeoutSecs) * time.Second,
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
}

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
