# jsfetch

A fast, header-aware JavaScript file downloader for web pages. Give it a URL and it fetches the page, finds every `<script src="...">` file, and downloads them all locally — with full support for authenticated pages via custom headers.

---

## Features

- Download all external JS files from any page
- Pass custom headers — cookies, auth tokens, anything
- Extract and save inline `<script>` blocks with `--inline`
- Concurrent downloads with `-c`
- List-only mode for piping into other tools
- Silent mode for clean output
- TLS verification skipped by default (handles self-signed certs)
- Duplicate filename handling — no overwrites

---

## Build

```bash
git clone https://github.com/Shaikh-Khizer/jsfetch.git
cd jsfetch/jsfetch
go mod init jsfetch
go get golang.org/x/net/html
go build -o jsfetch .
```

Move to PATH for global use:
```bash
mv jsfetch /usr/local/bin/
```

---

## Usage

```
jsfetch -u <URL> [options]
```

## CLI Options
### Core Options:
| Flag              | Description                               | Default        |
| ----------------- | ----------------------------------------- | -------------- |
| `-u <url>`        | Target page URL (required)                | —              |
| `-o <dir>`        | Output directory to save downloaded files | `js_output`    |
| `-H 'Key: Value'` | Custom header (repeatable)                | —              |
| `-ua <string>`    | User-Agent string                         | Mozilla/5.0... |
| `-t <seconds>`    | HTTP timeout                              | `45`           |
| `-X <method>`     | HTTP method for page request              | `GET`          |
| `-p <proxy>`      | Proxy URL (HTTP/SOCKS5)                   | —              |

### Download Options:
| Flag                      | Description                       | Default   |
| ------------------------- | --------------------------------- | --------- |
| `-c <workers>`            | Concurrent download workers       | `1`       |
| `--rate <ms>`             | Delay between requests per worker | `0`       |
| `--retry <n>`             | Retries on failed downloads       | `2`       |
| `--accept-status <range>` | Accept page status range          | `200-299` |

### Skip / Filter Options:
| Flag                   | Description                                 | Default |
| ---------------------- | ------------------------------------------- | ------- |
| `--no-skip`            | Disable built-in CDN/library skip list      | Enabled |
| `--skip-domain <host>` | Extra domain to skip (repeatable)           | —       |
| `--skip-pat <pattern>` | Extra filename pattern to skip (repeatable) | —       |

### Output / Display Options: 
| Flag          | Description                             | Default  |
| ------------- | --------------------------------------- | -------- |
| `--list-only` | Print discovered URLs only, no download | Disabled |
| `--inline`    | Save inline `<script>` blocks           | Disabled |
| `--silent`    | Suppress logs (clean output)            | Disabled |

## Built-in Skip List
| Domains                      |
| ---------------------------- |
| jsdelivr, cdnjs, unpkg       |
| googleapis, googletagmanager |
| bootstrapcdn, fontawesome    |
| cloudflareinsights, segment  |
| intercom, facebook, twitter  |
| linkedin, sentry             |

## Patterns
| Patterns                    |
| --------------------------- |
| jquery, react, vue, angular |
| bootstrap, lodash, moment   |
| axios, popper, tailwind     |
| polyfill, core-js, gtm      |

---

## Examples

### Basic download
```bash
jsfetch -u https://example.com/page
```

### With cookie authentication
```bash
jsfetch -u https://app.example.com/dashboard \
  -H "Cookie: session=abc123"
```

### Multiple headers
```bash
jsfetch -u https://app.example.com/dashboard \
  -H "Cookie: session=abc123" \
  -H "Authorization: Bearer eyJhbGc..."
```

### Custom output folder
```bash
jsfetch -u https://example.com/page \
  -H "Cookie: session=abc" \
  -o ./my_js_files
```

### Speed up with concurrent workers
```bash
# Download 10 files at a time
jsfetch -u https://example.com/page -c 10
```

### Also grab inline scripts
```bash
jsfetch -u https://example.com/page --inline
# Saves: js_output/inline_1.js, inline_2.js, ...
```

### List URLs only (no download)
```bash
jsfetch -u https://example.com/page --list-only
```

### Pipe into other tools
```bash
jsfetch -u https://example.com/page --list-only --silent | nuclei -t exposures/
jsfetch -u https://example.com/page --list-only --silent | httpx -silent
jsfetch -u https://example.com/page --list-only --silent | waybackurls
```

### Full example with all options
```bash
jsfetch -u https://app.example.com/settings \
  -H "Cookie: _auth=xyz" \
  -H "Authorization: Bearer token123" \
  -o ./output \
  -c 5 \
  -t 60 \
  --inline \
  --silent
```

---

## Output Structure

```
js_output/
├── runtime.bundle.js
├── app.bundle.js
├── ui.bundle.js
├── helpers.bundle.js
├── www.google.com_recaptcha.js   ← cross-origin files prefixed with host
├── inline_1.js                   ← only with --inline
├── inline_2.js
└── ...
```

- Cross-origin files are prefixed with their hostname to avoid collisions
- Duplicate filenames are automatically suffixed: `app.js`, `app_1.js`, `app_2.js`

---

## Tips

- Start with `-c 1` (default) — safe, one by one, server won't flag you
- Use `-c 10`+ only when you need speed and the server can handle it
- `--silent` + `--list-only` is the cleanest combo for piping into recon tools
- `--inline` is useful for SPAs where config/env data is embedded in the HTML

---

## Deobfuscating Downloaded Files

JS files downloaded from modern web apps are usually minified or obfuscated and hard to read. Use the included `deobfuscate.sh` script to clean them all up in place.

### Requirement

```bash
npm install -g webcrack
```

### Usage

```bash
chmod +x deobfuscate.sh

# default — deobfuscates everything inside ./js_output
./deobfuscate.sh

# custom folder
./deobfuscate.sh ./my_js_files

# help
./deobfuscate.sh -h
```

### What it does

- Runs `webcrack` on every `.js` file in the folder
- Overwrites each file in place — same filename, no copies
- If `webcrack` fails on a file, the **original is kept safe** and the error is printed
- Skips empty files automatically
- Prints a full summary at the end — success / failed / skipped

### Example output

```
[1/21] Processing: js_output/app.bundle.js
    [✓] Done — 4281234 bytes
[2/21] Processing: js_output/helpers.bundle.js
    [✓] Done — 151203 bytes
[3/21] Processing: js_output/broken.bundle.js
    [-] Failed (exit 1)
        Error: SyntaxError: Unexpected token

─────────────────────────────────────────
  Success : 20
  Failed  : 1
  Skipped : 0
  Total   : 21
─────────────────────────────────────────

[!] Files that failed:
    • js_output/broken.bundle.js (exit 1)
```

### Typical workflow

```bash
# Step 1 — download all JS files
jsfetch -u https://app.example.com/page \
  -H "Cookie: session=abc" \
  --inline \
  -o ./js_output

# Step 2 — deobfuscate everything in place
./deobfuscate.sh ./js_output

# Step 3 — grep for secrets, endpoints, tokens
grep -rE "(secret|token|key|password|api_key)\s*[:=]\s*['\"][^'\"]{8,}" js_output/
grep -rEo 'https?://[a-zA-Z0-9./_-]+' js_output/ | sort -u
```

---

## License

This project is licensed under the MIT License.  
See the [LICENSE](LICENSE) file for details.

---

## Author

**Shaikh Khizer**  
Computer Science Student | Penetration Tester
