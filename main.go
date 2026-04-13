package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// ─── Header flag (repeatable -H) ────────────────────────────────────────────

type HeaderFlags []string

func (h *HeaderFlags) String() string { return strings.Join(*h, ", ") }
func (h *HeaderFlags) Set(val string) error {
	*h = append(*h, val)
	return nil
}

// ─── Result from a download worker ──────────────────────────────────────────

type DownloadResult struct {
	URL      string
	SavePath string
	Bytes    int
	Err      error
}

// ─── Main ────────────────────────────────────────────────────────────────────

func main() {
	var headers HeaderFlags

	targetURL := flag.String("u", "", "Target URL (e.g. https://example.com/page)")
	outDir    := flag.String("o", "js_output", "Output directory to save JS files")
	timeout   := flag.Int("t", 45, "HTTP timeout in seconds")
	userAgent := flag.String("ua", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", "User-Agent")
	workers   := flag.Int("c", 1, "Number of concurrent download workers")
	listOnly  := flag.Bool("list-only", false, "Only print JS URLs, do not download (good for piping)")
	doInline  := flag.Bool("inline", false, "Also extract and save inline <script> blocks after external downloads")
	silent    := flag.Bool("silent", false, "Suppress all output except URLs (useful with --list-only)")

	flag.Var(&headers, "H", "Custom header 'Key: Value' (repeatable)")
	flag.Parse()

	if *targetURL == "" {
		fmt.Println("Usage: jsfetch -u <URL> [options]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  -u <url>          Target page URL (required)")
		fmt.Println("  -o <dir>          Output directory (default: js_output)")
		fmt.Println("  -H 'Key: Value'   Custom header, repeatable")
		fmt.Println("  -ua <string>      User-Agent string")
		fmt.Println("  -t <seconds>      HTTP timeout (default: 45)")
		fmt.Println("  -c <workers>      Concurrent download workers (default: 1)")
		fmt.Println("  --list-only       Print JS URLs only, no download")
		fmt.Println("  --inline          Also save inline <script> blocks as inline_N.js")
		fmt.Println("  --silent          Suppress banners/logs (clean output)")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  jsfetch -u https://example.com/page -H 'Cookie: sess=abc' -o ./out")
		fmt.Println("  jsfetch -u https://example.com/page --list-only --silent | nuclei -t exposures/")
		fmt.Println("  jsfetch -u https://example.com/page --inline -c 10")
		os.Exit(1)
	}

	log := func(format string, args ...any) {
		if !*silent {
			fmt.Printf(format+"\n", args...)
		}
	}

	// Build HTTP client
	client := &http.Client{
		Timeout: time.Duration(*timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Parse custom headers
	parsedHeaders := map[string]string{}
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			parsedHeaders[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	// Helper: build a request with all headers applied
	buildReq := func(u string) (*http.Request, error) {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", *userAgent)
		for k, v := range parsedHeaders {
			req.Header.Set(k, v)
		}
		return req, nil
	}

	// ── Step 1: Fetch the target page ────────────────────────────────────────

	log("[*] Fetching page : %s", *targetURL)

	req, err := buildReq(*targetURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Bad URL: %s\n", err)
		os.Exit(1)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Fetch failed: %s\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Read body failed: %s\n", err)
		os.Exit(1)
	}
	pageHTML := string(bodyBytes)

	baseURL, _ := url.Parse(*targetURL)

	// ── Step 2: Extract external <script src="..."> ───────────────────────────

	jsURLs := extractScriptSrcs(pageHTML, baseURL)
	log("[*] Found %d external JS file(s)", len(jsURLs))

	// ── --list-only: just print and exit ─────────────────────────────────────

	if *listOnly {
		for _, u := range jsURLs {
			fmt.Println(u)
		}
		if *doInline {
			inlines := extractInlineScripts(pageHTML)
			log("[*] Found %d inline script(s)", len(inlines))
			for i := range inlines {
				fmt.Printf("[inline_%d]\n", i+1)
			}
		}
		os.Exit(0)
	}

	if len(jsURLs) == 0 && !*doInline {
		log("[-] No JS files found on the page.")
		os.Exit(0)
	}

	// ── Step 3: Create output dir ─────────────────────────────────────────────

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Cannot create output dir: %s\n", err)
		os.Exit(1)
	}

	// ── Step 4: Concurrent download of external JS files ─────────────────────

	type Job struct {
		jsURL    string
		outPath  string
	}

	jobs    := make(chan Job, len(jsURLs))
	results := make(chan DownloadResult, len(jsURLs))

	// Resolve filenames up front (serial) so dedup is deterministic
	usedNames := map[string]bool{}
	var jobList []Job
	for _, jsURL := range jsURLs {
		parsed, _ := url.Parse(jsURL)
		fname := filepath.Base(parsed.Path)
		if fname == "" || fname == "." || fname == "/" {
			fname = "script.js"
		}
		if parsed.Host != baseURL.Host {
			fname = parsed.Host + "_" + fname
		}
		outPath := uniquePath(filepath.Join(*outDir, fname), usedNames)
		usedNames[filepath.Base(outPath)] = true
		jobList = append(jobList, Job{jsURL: jsURL, outPath: outPath})
	}

	// Spawn workers
	numWorkers := *workers
	if numWorkers < 1 {
		numWorkers = 1
	}
	if numWorkers > len(jobList) && len(jobList) > 0 {
		numWorkers = len(jobList)
	}

	log("[*] Downloading with %d worker(s)...\n", numWorkers)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				req, err := buildReq(job.jsURL)
				if err != nil {
					results <- DownloadResult{URL: job.jsURL, Err: err}
					continue
				}
				resp, err := client.Do(req)
				if err != nil {
					results <- DownloadResult{URL: job.jsURL, Err: err}
					continue
				}
				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					results <- DownloadResult{URL: job.jsURL, Err: err}
					continue
				}
				if err := os.WriteFile(job.outPath, data, 0644); err != nil {
					results <- DownloadResult{URL: job.jsURL, Err: err}
					continue
				}
				results <- DownloadResult{URL: job.jsURL, SavePath: job.outPath, Bytes: len(data)}
			}
		}()
	}

	// Feed jobs
	for _, job := range jobList {
		jobs <- job
	}
	close(jobs)

	// Collect results in a separate goroutine so workers never block
	go func() {
		wg.Wait()
		close(results)
	}()

	successCount := 0
	for r := range results {
		if r.Err != nil {
			log("    [-] FAIL  %s  (%s)", r.URL, r.Err)
		} else {
			log("    [✓] Saved  %s  ->  %s  (%d bytes)", r.URL, r.SavePath, r.Bytes)
			successCount++
		}
	}

	log("\n[*] External JS: %d/%d downloaded to ./%s/", successCount, len(jsURLs), *outDir)

	// ── Step 5: Inline scripts (only if --inline passed) ─────────────────────

	if !*doInline {
		return
	}

	log("\n[*] Extracting inline <script> blocks...")

	inlines := extractInlineScripts(pageHTML)
	log("[*] Found %d inline script(s)", len(inlines))

	if len(inlines) == 0 {
		log("[-] No inline scripts found.")
		return
	}

	inlineCount := 0
	for i, code := range inlines {
		fname    := fmt.Sprintf("inline_%d.js", i+1)
		outPath  := filepath.Join(*outDir, fname)
		if err := os.WriteFile(outPath, []byte(code), 0644); err != nil {
			log("    [-] Save failed for %s: %s", fname, err)
			continue
		}
		log("    [✓] Saved  %s  (%d bytes)", outPath, len(code))
		inlineCount++
	}

	log("\n[*] Inline JS : %d/%d saved to ./%s/", inlineCount, len(inlines), *outDir)
}

// ─── Extract external <script src="..."> ────────────────────────────────────

func extractScriptSrcs(body string, baseURL *url.URL) []string {
	var jsURLs []string
	seen := map[string]bool{}

	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			tok := tokenizer.Token()
			if tok.Data == "script" {
				for _, attr := range tok.Attr {
					if attr.Key == "src" && attr.Val != "" {
						resolved := resolveURL(baseURL, attr.Val)
						if resolved != "" && !seen[resolved] {
							seen[resolved] = true
							jsURLs = append(jsURLs, resolved)
						}
					}
				}
			}
		}
	}
	return jsURLs
}

// ─── Extract inline <script> content (no src attr) ──────────────────────────

func extractInlineScripts(body string) []string {
	var scripts []string

	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}

		if tt == html.StartTagToken {
			tok := tokenizer.Token()
			if tok.Data != "script" {
				continue
			}
			// Skip if it has a src attribute
			hasSrc := false
			for _, attr := range tok.Attr {
				if attr.Key == "src" {
					hasSrc = true
					break
				}
			}
			if hasSrc {
				continue
			}
			// Next token should be the text content
			inner := tokenizer.Next()
			if inner == html.TextToken {
				code := strings.TrimSpace(string(tokenizer.Text()))
				if code != "" {
					scripts = append(scripts, code)
				}
			}
		}
	}
	return scripts
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func resolveURL(base *url.URL, ref string) string {
	if strings.HasPrefix(ref, "//") {
		return base.Scheme + ":" + ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return base.ResolveReference(refURL).String()
}

func uniquePath(path string, used map[string]bool) string {
	base := filepath.Base(path)
	if !used[base] {
		return path
	}
	dir  := filepath.Dir(path)
	ext  := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, i, ext)
		if !used[candidate] {
			return filepath.Join(dir, candidate)
		}
	}
}
