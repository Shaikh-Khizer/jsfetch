package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func main() {
	var headers HeaderFlags

	targetURL    := flag.String("u", "", "Target URL (e.g. https://example.com/page)")
	outDir       := flag.String("o", "js_output", "Output directory to save downloaded files")
	timeout      := flag.Int("t", 45, "HTTP timeout in seconds")
	userAgent    := flag.String("ua", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", "User-Agent string")
	workers      := flag.Int("c", 1, "Number of concurrent download workers")
	rateLimit    := flag.Int("rate", 0, "Min milliseconds between requests per worker (0 = no limit)")
	retries      := flag.Int("retry", 2, "Number of retries on failed downloads")
	listOnly     := flag.Bool("list-only", false, "Only print discovered URLs, do not download")
	doInline     := flag.Bool("inline", false, "Also extract and save inline <script> blocks")
	silent       := flag.Bool("silent", false, "Suppress all output except URLs (useful with --list-only)")
	statusRangeS := flag.String("accept-status", "200-299", "Download only if page status is in this range (e.g. 200-302)")

	flag.Var(&headers, "H", "Custom header 'Key: Value' (repeatable)")
	flag.Parse()

	if *targetURL == "" {
		printUsage()
		os.Exit(1)
	}

	// ── Parse status range ────────────────────────────────────────────────────

	acceptRange, err := parseStatusRange(*statusRangeS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] --accept-status: %s\n", err)
		os.Exit(1)
	}

	// ── Logger ────────────────────────────────────────────────────────────────

	log := func(format string, args ...any) {
		if !*silent {
			fmt.Printf(format+"\n", args...)
		}
	}

	// ── HTTP client & request builder ─────────────────────────────────────────

	client     := newHTTPClient(*timeout)
	buildReq   := requestBuilder(*userAgent, parseHeaders(headers))

	// ── Step 1: Fetch the target page ─────────────────────────────────────────

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

	log("[*] Page status  : %d %s", resp.StatusCode, statusText(resp.StatusCode))

	if !acceptRange.Contains(resp.StatusCode) {
		fmt.Fprintf(os.Stderr,
			"[-] Status %d is outside accepted range %d-%d — aborting.\n",
			resp.StatusCode, acceptRange.Lo, acceptRange.Hi,
		)
		os.Exit(1)
	}
	log("[✓] Status %d is within accepted range %d-%d — proceeding.\n",
		resp.StatusCode, acceptRange.Lo, acceptRange.Hi)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Read body failed: %s\n", err)
		os.Exit(1)
	}
	pageHTML := string(bodyBytes)
	baseURL, _ := url.Parse(*targetURL)

	// ── Step 2: Extract external JS ───────────────────────────────────────────

	jsURLs, scriptErrs := extractScriptSrcs(pageHTML, baseURL)
	log("[*] Found %d external JS file(s)", len(jsURLs))
	if len(scriptErrs) > 0 {
		log("[!] %d script src(s) could not be parsed:", len(scriptErrs))
		for _, e := range scriptErrs {
			log("    [-] %s", e)
		}
		log("")
	}

	// ── Step 3: Extract <link> assets ─────────────────────────────────────────

	linkAssets, linkErrs := extractLinkAssets(pageHTML, baseURL)
	log("[*] Found %d <link> asset(s)", len(linkAssets))
	if len(linkErrs) > 0 {
		log("[!] %d link href(s) could not be parsed:", len(linkErrs))
		for _, e := range linkErrs {
			log("    [-] %s", e)
		}
		log("")
	}

	// ── --list-only ───────────────────────────────────────────────────────────

	if *listOnly {
		fmt.Println("=== JS files ===")
		for _, u := range jsURLs {
			fmt.Println(u)
		}
		fmt.Println("\n=== <link> assets ===")
		for _, a := range linkAssets {
			if a.Rel != "" {
				fmt.Printf("[%s] %s\n", a.Rel, a.URL)
			} else {
				fmt.Println(a.URL)
			}
		}
		if *doInline {
			inlines := extractInlineScripts(pageHTML)
			fmt.Printf("\n=== Inline scripts (%d) ===\n", len(inlines))
			for i, code := range inlines {
				fmt.Printf("[inline_%d] (%d bytes)\n%s\n\n", i+1, len(code), code)
			}
		}
		os.Exit(0)
	}

	if len(jsURLs) == 0 && len(linkAssets) == 0 && !*doInline {
		log("[-] Nothing found to download.")
		os.Exit(0)
	}

	// ── Step 4: Create output dir ─────────────────────────────────────────────

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Cannot create output dir: %s\n", err)
		os.Exit(1)
	}

	// ── Step 5: Build job list ────────────────────────────────────────────────

	usedNames := map[string]bool{}

	buildJobs := func(urls []string) []Job {
		var jobs []Job
		for _, srcURL := range urls {
			parsed, _ := url.Parse(srcURL)
			fname := filepath.Base(parsed.Path)
			if fname == "" || fname == "." || fname == "/" {
				fname = "script.js"
			}
			if parsed.Host != baseURL.Host {
				fname = parsed.Host + "_" + fname
			}
			outPath := uniquePath(filepath.Join(*outDir, fname), usedNames)
			usedNames[filepath.Base(outPath)] = true
			jobs = append(jobs, Job{srcURL: srcURL, outPath: outPath})
		}
		return jobs
	}

	var allJobs []Job
	allJobs = append(allJobs, buildJobs(jsURLs)...)

	linkURLs := make([]string, len(linkAssets))
	for i, a := range linkAssets {
		linkURLs[i] = a.URL
	}
	allJobs = append(allJobs, buildJobs(linkURLs)...)

	// ── Step 6: Download ──────────────────────────────────────────────────────

	log("[*] Downloading %d file(s) with %d worker(s)...\n", len(allJobs), *workers)

	results := RunDownloads(
		client,
		buildReq,
		allJobs,
		*workers,
		time.Duration(*rateLimit)*time.Millisecond,
		*retries,
		log,
	)

	successCount := 0
	for _, r := range results {
		if r.Err != nil {
			log("    [-] FAIL  %s  (%s)", r.URL, r.Err)
		} else {
			log("    [✓] Saved  %s  →  %s  (%d bytes)", r.URL, r.SavePath, r.Bytes)
			successCount++
		}
	}

	log("\n[*] Downloaded %d/%d file(s) to ./%s/", successCount, len(allJobs), *outDir)

	// ── Step 7: Inline scripts ────────────────────────────────────────────────

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
		fname   := fmt.Sprintf("inline_%d.js", i+1)
		outPath := filepath.Join(*outDir, fname)
		if err := os.WriteFile(outPath, []byte(code), 0644); err != nil {
			log("    [-] Save failed for %s: %s", fname, err)
			continue
		}
		log("    [✓] Saved  %s  (%d bytes)", outPath, len(code))
		inlineCount++
	}

	log("\n[*] Inline JS : %d/%d saved to ./%s/", inlineCount, len(inlines), *outDir)
}

// statusText wraps http.StatusText to avoid importing net/http in main just for this.
func statusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 301:
		return "Moved Permanently"
	case 302:
		return "Found"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 500:
		return "Internal Server Error"
	default:
		return ""
	}
}

func printUsage() {
	fmt.Println("Usage: jsfetch -u <URL> [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -u <url>              Target page URL (required)")
	fmt.Println("  -o <dir>              Output directory (default: output)")
	fmt.Println("  -H 'Key: Value'       Custom header, repeatable")
	fmt.Println("  -ua <string>          User-Agent string")
	fmt.Println("  -t <seconds>          HTTP timeout (default: 45)")
	fmt.Println("  -c <workers>          Concurrent download workers (default: 1)")
	fmt.Println("  --rate <ms>           Min delay between requests per worker in ms (default: 0)")
	fmt.Println("  --retry <n>           Retries on failed downloads (default: 2)")
	fmt.Println("  --accept-status <r>   Download only if page status in range (default: 200-299)")
	fmt.Println("                        Examples: 200-299  |  200-302  |  200-399")
	fmt.Println("  --list-only           Print discovered URLs only, no download")
	fmt.Println("  --inline              Also save inline <script> blocks as inline_N.js")
	fmt.Println("  --silent              Suppress banners/logs (clean output for piping)")
	fmt.Println()
	fmt.Println("Output layout:")
	fmt.Println("  All files saved directly into <outdir>/ (default: js_output)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  jsfetch -u https://example.com -o ./out")
	fmt.Println("  jsfetch -u https://example.com -H 'Cookie: sess=abc' -c 5 --rate 200")
	fmt.Println("  jsfetch -u https://example.com --accept-status 200-302")
	fmt.Println("  jsfetch -u https://example.com --list-only --silent | grep stylesheet")
	fmt.Println("  jsfetch -u https://example.com --inline --retry 3")
}
