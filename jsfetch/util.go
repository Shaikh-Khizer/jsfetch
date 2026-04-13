package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ─── Header flag (repeatable -H) ─────────────────────────────────────────────

type HeaderFlags []string

func (h *HeaderFlags) String() string { return strings.Join(*h, ", ") }
func (h *HeaderFlags) Set(val string) error {
	*h = append(*h, val)
	return nil
}

// ─── Result from a download worker ───────────────────────────────────────────

type DownloadResult struct {
	URL      string
	SavePath string
	Bytes    int
	Err      error
}

// ─── Job passed to download workers ──────────────────────────────────────────

type Job struct {
	srcURL  string
	outPath string
}

// ─── Status range flag ────────────────────────────────────────────────────────
// Parses a string like "200-302" into lo/hi ints.

type StatusRange struct {
	Lo int
	Hi int
}

func parseStatusRange(s string) (StatusRange, error) {
	var lo, hi int
	_, err := fmt.Sscanf(s, "%d-%d", &lo, &hi)
	if err != nil || lo < 100 || hi > 599 || lo > hi {
		return StatusRange{}, fmt.Errorf("invalid status range %q — use format like 200-299 or 200-302", s)
	}
	return StatusRange{Lo: lo, Hi: hi}, nil
}

func (sr StatusRange) Contains(code int) bool {
	return code >= sr.Lo && code <= sr.Hi
}

// ─── URL helpers ──────────────────────────────────────────────────────────────

// resolveURL resolves a possibly-relative href against a base URL.
func resolveURL(base *url.URL, ref string) (string, error) {
	if strings.HasPrefix(ref, "//") {
		ref = base.Scheme + ":" + ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(refURL).String(), nil
}

// uniquePath appends _1, _2, … when a filename already exists in the used map.
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
