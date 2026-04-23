package main

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// ─── External <script src="..."> ─────────────────────────────────────────────

// extractScriptSrcs returns all resolved external JS URLs from <script src>.
// Parse errors are returned separately so the caller can report them.
func extractScriptSrcs(body string, baseURL *url.URL) ([]string, []string) {
	var jsURLs []string
	var parseErrs []string
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
						resolved, err := resolveURL(baseURL, attr.Val)
						if err != nil {
							parseErrs = append(parseErrs, fmt.Sprintf("bad src %q: %s", attr.Val, err))
							continue
						}
						if !seen[resolved] {
							seen[resolved] = true
							jsURLs = append(jsURLs, resolved)
						}
					}
				}
			}
		}
	}
	return jsURLs, parseErrs
}

// ─── Inline <script> blocks ───────────────────────────────────────────────────

// extractInlineScripts returns the text content of every <script> that has
// no src attribute.
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
			if inner := tokenizer.Next(); inner == html.TextToken {
				code := strings.TrimSpace(string(tokenizer.Text()))
				if code != "" {
					scripts = append(scripts, code)
				}
			}
		}
	}
	return scripts
}

// ─── <link href="..."> tags ───────────────────────────────────────────────────

// LinkAsset holds a resolved URL and the rel/type attributes of a <link> tag.
type LinkAsset struct {
	URL string
	Rel string
}

// extractLinkAssets returns all <link href> resources (stylesheets, fonts,
// manifests, icons, …) resolved against baseURL.
// Skips tags that have no href or whose href is a data-URI.
func extractLinkAssets(body string, baseURL *url.URL) ([]LinkAsset, []string) {
	var assets []LinkAsset
	var parseErrs []string
	seen := map[string]bool{}

	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := tokenizer.Token()
		if tok.Data != "link" {
			continue
		}

		var href, rel string
		for _, attr := range tok.Attr {
			switch attr.Key {
			case "href":
				href = strings.TrimSpace(attr.Val)
			case "rel":
				rel = strings.TrimSpace(attr.Val)
			}
		}

		if href == "" || strings.HasPrefix(href, "data:") {
			continue
		}

		// Only download <link> tags that point to JS files.
		if !strings.HasSuffix(strings.ToLower(strings.Split(href, "?")[0]), ".js") {
			continue
		}

		resolved, err := resolveURL(baseURL, href)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("bad link href %q: %s", href, err))
			continue
		}

		if !seen[resolved] {
			seen[resolved] = true
			assets = append(assets, LinkAsset{URL: resolved, Rel: rel})
		}
	}
	return assets, parseErrs
}
