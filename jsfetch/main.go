package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var headers      HeaderFlags
	var skipDomains  StringSliceFlag
	var skipPatterns StringSliceFlag

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

	// ── New flags ─────────────────────────────────────────────────────────────

	proxyAddr := flag.String("p", "", "Proxy URL (e.g. http://127.0.0.1:8080 or socks5://127.0.0.1:1080)")
	method    := flag.String("X", "GET", "HTTP method for the initial page request (e.g. POST, HEAD)")
	noSkip    := flag.Bool("no-skip", false, "Disable the built-in CDN / library skip list (download everything)")
	insecure := flag.Bool("k", false, "Skip TLS certificate verification (insecure)")

	flag.Var(&headers,      "H",           "Custom header 'Key: Value' (repeatable)")
	flag.Var(&skipDomains,  "skip-domain", "Extra domain to skip, e.g. cdn.example.com (repeatable)")
	flag.Var(&skipPatterns, "skip-pat",    "Extra filename pattern to skip, e.g. analytics (repeatable)")
	
	flag.Usage = printUsage
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

	// ── Parse proxy ───────────────────────────────────────────────────────────

	var proxyURL *url.URL
	if *proxyAddr != "" {
		proxyURL, err = url.Parse(*proxyAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] -p: invalid proxy URL: %s\n", err)
			os.Exit(1)
		}
	}

	// ── Normalise method ──────────────────────────────────────────────────────

	*method = strings.ToUpper(strings.TrimSpace(*method))
	if *method == "" {
		*method = "GET"
	}

	// ── Logger ────────────────────────────────────────────────────────────────

	log := func(format string, args ...any) {
		if !*silent {
			fmt.Printf(format+"\n", args...)
		}
	}

	// ── Skip filter ───────────────────────────────────────────────────────────

	sf := &SkipFilter{
		Disabled:      *noSkip,
		ExtraDomains:  []string(skipDomains),
		ExtraPatterns: []string(skipPatterns),
	}
	if sf.Disabled {
		log("[~] Skip filter  : disabled (--no-skip)")
	} else {
		log("[~] Skip filter  : ON  (pass --no-skip to disable, --skip-domain / --skip-pat to extend)")
	}

	// ── HTTP client ───────────────────────────────────────────────────────────
	//   buildPageReq   — uses the user-specified method (-X), for the initial fetch
	//   buildDlReq     — always GET, for downloading JS assets

	client 		 := newHTTPClient(*timeout, proxyURL, *insecure)
	parsedHdrs   := parseHeaders(headers)
	buildPageReq := requestBuilder(*method, *userAgent, parsedHdrs)
	buildDlReq   := requestBuilder("GET",   *userAgent, parsedHdrs)

	if proxyURL != nil {
		log("[~] Proxy        : %s", proxyURL.String())
	}
	if *method != "GET" {
		log("[~] Page method  : %s", *method)
	}

	// ── Step 1: Fetch the target page ─────────────────────────────────────────

	log("[*] Fetching page : %s", *targetURL)

	req, err := buildPageReq(*targetURL)
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
	log("[*] Found %d external JS file(s) (pre-filter)", len(jsURLs))
	if len(scriptErrs) > 0 {
		log("[!] %d script src(s) could not be parsed:", len(scriptErrs))
		for _, e := range scriptErrs {
			log("    [-] %s", e)
		}
		log("")
	}

	jsURLs, skippedJS := filterURLs(jsURLs, sf, baseURL.Host)
	log("[*] Kept  %d external JS file(s) (post-filter)", len(jsURLs))

	// ── Step 3: Extract <link> assets ─────────────────────────────────────────

	linkAssets, linkErrs := extractLinkAssets(pageHTML, baseURL)
	log("[*] Found %d <link> asset(s) (pre-filter)", len(linkAssets))
	if len(linkErrs) > 0 {
		log("[!] %d link href(s) could not be parsed:", len(linkErrs))
		for _, e := range linkErrs {
			log("    [-] %s", e)
		}
		log("")
	}

	// Filter link assets
	rawLinkURLs := make([]string, len(linkAssets))
	for i, a := range linkAssets {
		rawLinkURLs[i] = a.URL
	}
	filteredLinkURLs, skippedLinks := filterURLs(rawLinkURLs, sf, baseURL.Host)
	filteredLinkSet := make(map[string]bool, len(filteredLinkURLs))
	for _, u := range filteredLinkURLs {
		filteredLinkSet[u] = true
	}
	var filteredLinkAssets []LinkAsset
	for _, a := range linkAssets {
		if filteredLinkSet[a.URL] {
			filteredLinkAssets = append(filteredLinkAssets, a)
		}
	}
	linkAssets = filteredLinkAssets
	log("[*] Kept  %d <link> asset(s) (post-filter)", len(linkAssets))

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
		buildDlReq,
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
		totalSkipped := append(skippedJS, skippedLinks...)

		if len(totalSkipped) > 0 {
			log("\n[*] Skipped %d file(s):", len(totalSkipped))
			for _, s := range totalSkipped {
				log("    [-] %s  — %s", s.URL, s.Reason)
			}
		}
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
	
	totalSkipped := append(skippedJS, skippedLinks...)

	if len(totalSkipped) > 0 {
		log("\n[*] Skipped %d file(s):", len(totalSkipped))
		for _, s := range totalSkipped {
			log("    [-] %s  — %s", s.URL, s.Reason)
		}
	}
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
	fmt.Println("Core options:")
	fmt.Println("  -u <url>                Target page URL (required)")
	fmt.Println("  -o <dir>                Output directory (default: js_output)")
	fmt.Println("  -H 'Key: Value'         Custom header, repeatable")
	fmt.Println("  -ua <string>            User-Agent string")
	fmt.Println("  -t <seconds>            HTTP timeout (default: 45)")
	fmt.Println("  -X <method>             HTTP method for the page request (default: GET)")
	fmt.Println("  -p <proxy>              Proxy URL, e.g. http://127.0.0.1:8080")
	fmt.Println("                          or socks5://127.0.0.1:1080")
	fmt.Println()
	fmt.Println("Download options:")
	fmt.Println("  -c <workers>            Concurrent download workers (default: 1)")
	fmt.Println("  --rate <ms>             Min delay between requests per worker (default: 0)")
	fmt.Println("  --retry <n>             Retries on failed downloads (default: 2)")
	fmt.Println("  --accept-status <r>     Accept page status in range (default: 200-299)")
	fmt.Println("                          Examples: 200-299  |  200-302  |  200-399")
	fmt.Println()
	fmt.Println("Skip / filter options (skip filter is ON by default):")
	fmt.Println("  --no-skip               Disable the built-in CDN/library skip list entirely")
	fmt.Println("  --skip-domain <host>    Extra domain to skip (repeatable)")
	fmt.Println("                          e.g. --skip-domain cdn.example.com")
	fmt.Println("  --skip-pat <pattern>    Extra filename pattern to skip (repeatable)")
	fmt.Println("                          e.g. --skip-pat analytics --skip-pat tracking")
	fmt.Println()
	fmt.Println("Output / display options:")
	fmt.Println("  --list-only             Print discovered URLs only, no download")
	fmt.Println("  --inline                Also save inline <script> blocks as inline_N.js")
	fmt.Println("  --silent                Suppress banners/logs (clean output for piping)")
	fmt.Println()
	fmt.Println("Built-in skip list covers:")
	fmt.Println("  Domains : jsdelivr, cdnjs, unpkg, googleapis, googletagmanager,")
	fmt.Println("            bootstrapcdn, fontawesome, cloudflareinsights, segment,")
	fmt.Println("            intercom, facebook, twitter, linkedin, sentry, …")
	fmt.Println("  Patterns: jquery, react, vue, angular, bootstrap, lodash, moment,")
	fmt.Println("            axios, popper, tailwind, polyfill, core-js, gtm, …")
	fmt.Println()
}
