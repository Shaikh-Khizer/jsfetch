package main

import (
	"net/url"
	"strings"
)

// defaultSkipDomains are CDN / analytics / social hosts whose JS is almost
// never application-specific code.  Applied when --no-skip is NOT set.
var defaultSkipDomains = []string{
	// Generic CDN / package registries
	"cdn.jsdelivr.net",
	"cdnjs.cloudflare.com",
	"unpkg.com",
	"cdn.skypack.dev",
	"esm.sh",
	"esm.run",

	// Google
	"ajax.googleapis.com",
	"apis.google.com",
	"www.google-analytics.com",
	"www.googletagmanager.com",
	"static.doubleclick.net",

	// jQuery / Bootstrap CDNs
	"code.jquery.com",
	"maxcdn.bootstrapcdn.com",
	"stackpath.bootstrapcdn.com",
	"netdna.bootstrapcdn.com",

	// Font / icon CDNs  (may serve .js too)
	"use.fontawesome.com",
	"kit.fontawesome.com",
	"fonts.googleapis.com",
	"fonts.gstatic.com",

	// Analytics / tracking
	"static.cloudflareinsights.com",
	"cdn.segment.com",
	"js.intercomcdn.com",
	"cdn.amplitude.com",
	"cdn.heapanalytics.com",
	"cdn.ravenjs.com",      // old Sentry
	"browser.sentry-cdn.com",

	// Social / ad platforms
	"connect.facebook.net",
	"platform.twitter.com",
	"platform.linkedin.com",
	"snap.licdn.com",
	"assets.pinterest.com",
	"www.recaptcha.net",
	"www.gstatic.com",
}

// defaultSkipPatterns are filename substrings that identify well-known
// third-party libraries regardless of which host serves them.
// Matching is case-insensitive on the base filename only.
var defaultSkipPatterns = []string{
	"jquery",
	"react.",
	"react-dom",
	"react.production",
	"react.development",
	"vue.",
	"vue.min",
	"angular.",
	"angularjs",
	"bootstrap.",
	"lodash.",
	"underscore.",
	"moment.",
	"dayjs.",
	"axios.",
	"popper.",
	"fontawesome",
	"font-awesome",
	"tailwind.",
	"bulma.",
	"semantic.",
	"materialize.",
	"polyfill.",
	"core-js",
	"regenerator-runtime",
	"gtm.",
	"ga.",
	"fbevents.",   
	"recaptcha",
	"sentry.",
	"amplitude.",
	"intercom.",
	"hotjar.",
	"crisp.",
	"drift.",
	"tawk.",
	"smartlook.",
	"fullstory.",
}

// ─── SkipFilter ───────────────────────────────────────────────────────────────

// SkipFilter decides whether a discovered URL should be excluded from
// downloading.  It is enabled by default; pass --no-skip to disable it.
type SkipFilter struct {
	Disabled      bool
	ExtraDomains  []string
	ExtraPatterns []string
}
type SkippedItem struct {
	URL    string
	Reason string
}
// ShouldSkip returns (true, reason) when the URL matches any active rule.
// When the filter is disabled it always returns (false, "").
func (sf *SkipFilter) ShouldSkip(rawURL string, baseHost string) (bool, string) {
	if sf.Disabled {
		return false, ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, ""
	}

	host := strings.ToLower(parsed.Hostname())

	// ── 1. Strict third-party domain filtering ───────────────────────────────
	if baseHost != "" && host != baseHost {
		return true, "third-party domain"
	}

	// ── 2. Known CDN / analytics domains ─────────────────────────────────────
	allDomains := append(defaultSkipDomains, sf.ExtraDomains...)
	for _, d := range allDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true, "CDN domain (" + d + ")"
		}
	}

	// ── 3. Filename extraction ───────────────────────────────────────────────
	path := parsed.Path
	if i := strings.Index(path, "?"); i != -1 {
		path = path[:i]
	}
	fname := strings.ToLower(path[strings.LastIndex(path, "/")+1:])

	// ── 4. HARD filters (high-confidence junk) ───────────────────────────────
	if strings.Contains(fname, ".min.") {
		return true, "minified library"
	}

	if strings.Contains(fname, ".vendor.") {
		return true, "vendor bundle"
	}

	if strings.Contains(fname, ".chunk.") {
		return true, "webpack chunk"
	}

	// ── 5. Framework / library detection (strong match) ──────────────────────
	frameworks := []string{
		"react", "vue", "angular", "next", "nuxt", "svelte",
		"ember", "backbone", "preact", "alpine",
		"jquery", "lodash", "moment", "axios",
		"graphql", "highcharts", "markdown",
	}

	for _, fw := range frameworks {
		if strings.Contains(fname, fw) {
			return true, "framework (" + fw + ")"
		}
	}

	// ── 6. Generic patterns (your existing + extras) ─────────────────────────
	allPatterns := append(defaultSkipPatterns, sf.ExtraPatterns...)
	extra := []string{
		"vendor",
		"bundle",
		"chunk",
		"runtime",
		"polyfill",
		"lib",
	}
	allPatterns = append(allPatterns, extra...)

	for _, p := range allPatterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if strings.Contains(fname, p) {
			return true, "pattern (" + p + ")"
		}
	}

	return false, ""
}

// filterURLs applies sf to a slice of raw URL strings.
// Skipped entries are logged; the accepted slice is returned.
func filterURLs(urls []string, sf *SkipFilter, baseHost string) ([]string, []SkippedItem) {
	if sf.Disabled {
		return urls, nil
	}

	var out []string
	var skipped []SkippedItem

	for _, u := range urls {
		if skip, reason := sf.ShouldSkip(u, baseHost); skip {
			skipped = append(skipped, SkippedItem{
				URL:    u,
				Reason: reason,
			})
		} else {
			out = append(out, u)
		}
	}

	return out, skipped
}
