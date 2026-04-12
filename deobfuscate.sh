#!/usr/bin/env bash
# ─────────────────────────────────────────────
#  deobfuscate.sh — Deobfuscate all JS files
#  using webcrack, in place, with error handling
#  Usage: ./deobfuscate.sh [directory]
#  Default directory: js_output
# ─────────────────────────────────────────────

# ── Config ────────────────────────────────────
DIR="${1:-js_output}"       # use first arg or default to js_output
SUCCESS=0
FAILED=0
SKIPPED=0
ERRORS=()

# ── Colors ────────────────────────────────────
GREEN="\033[0;32m"
RED="\033[0;31m"
YELLOW="\033[0;33m"
CYAN="\033[0;36m"
RESET="\033[0m"

# ── Help ─────────────────────────────────────
if [[ "$1" == "-h" || "$1" == "--help" ]]; then
  echo -e "${CYAN}"
  echo "  ██████╗ ███████╗ ██████╗ ██████╗ "
  echo "  ██╔══██╗██╔════╝██╔═══██╗██╔══██╗"
  echo "  ██║  ██║█████╗  ██║   ██║██████╔╝"
  echo "  ██║  ██║██╔══╝  ██║   ██║██╔══██╗"
  echo "  ██████╔╝███████╗╚██████╔╝██████╔╝"
  echo "  ╚═════╝ ╚══════╝ ╚═════╝ ╚═════╝ "
  echo -e "${RESET}"
  echo -e "${CYAN}  JS Deobfuscator using webcrack${RESET}"
  echo ""
  echo -e "  ${CYAN}Usage:${RESET}"
  echo -e "    ./deobfuscate.sh [directory]"
  echo -e "    ./deobfuscate.sh -h"
  echo ""
  echo -e "  ${CYAN}Arguments:${RESET}"
  echo -e "    directory     Path to folder containing .js files"
  echo -e "                  Default: js_output"
  echo ""
  echo -e "  ${CYAN}Examples:${RESET}"
  echo -e "    ./deobfuscate.sh                  # uses ./js_output"
  echo -e "    ./deobfuscate.sh ./my_js_files    # custom folder"
  echo -e "    ./deobfuscate.sh /tmp/target_js   # absolute path"
  echo ""
  echo -e "  ${CYAN}Notes:${RESET}"
  echo -e "    • Files are deobfuscated in place (same filename)"
  echo -e "    • Original is kept safe if webcrack fails"
  echo -e "    • Empty files are skipped automatically"
  echo -e "    • Requires webcrack: npm install -g webcrack"
  echo ""
  exit 0
fi

# ── Checks ────────────────────────────────────

# Check webcrack is installed
if ! command -v webcrack &>/dev/null; then
  echo -e "${RED}[-] webcrack not found. Install it with:${RESET}"
  echo -e "    npm install -g webcrack"
  exit 1
fi

# Check directory exists
if [ ! -d "$DIR" ]; then
  echo -e "${RED}[-] Directory not found: $DIR${RESET}"
  exit 1
fi

# Check there are JS files
JS_FILES=("$DIR"/*.js)
if [ ! -e "${JS_FILES[0]}" ]; then
  echo -e "${YELLOW}[!] No .js files found in: $DIR${RESET}"
  exit 0
fi

TOTAL=${#JS_FILES[@]}

# ── Banner ────────────────────────────────────
echo -e "${CYAN}"
echo "  ██████╗ ███████╗ ██████╗ ██████╗ "
echo "  ██╔══██╗██╔════╝██╔═══██╗██╔══██╗"
echo "  ██║  ██║█████╗  ██║   ██║██████╔╝"
echo "  ██║  ██║██╔══╝  ██║   ██║██╔══██╗"
echo "  ██████╔╝███████╗╚██████╔╝██████╔╝"
echo "  ╚═════╝ ╚══════╝ ╚═════╝ ╚═════╝ "
echo -e "${RESET}"
echo -e "${CYAN}  JS Deobfuscator using webcrack${RESET}"
echo -e "  Directory : ${DIR}"
echo -e "  Files     : ${TOTAL}"
echo ""

# ── Main Loop ─────────────────────────────────
INDEX=0
for f in "${JS_FILES[@]}"; do
  INDEX=$((INDEX + 1))

  # Skip if not a regular file
  if [ ! -f "$f" ]; then
    echo -e "${YELLOW}[!] Skipping (not a file): $f${RESET}"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  # Skip empty files
  if [ ! -s "$f" ]; then
    echo -e "${YELLOW}[!] Skipping (empty file): $f${RESET}"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  echo -e "${CYAN}[${INDEX}/${TOTAL}] Processing: $f${RESET}"

  TMPFILE="${f}.tmp"

  # Run webcrack, capture stderr separately
  ERROR_MSG=$(webcrack "$f" > "$TMPFILE" 2>&1)
  EXIT_CODE=$?

  # webcrack exits 0 on success
  if [ $EXIT_CODE -ne 0 ]; then
    echo -e "    ${RED}[-] Failed (exit $EXIT_CODE)${RESET}"
    # Print the error message
    if [ -n "$ERROR_MSG" ]; then
      echo -e "    ${RED}    Error: $ERROR_MSG${RESET}"
    fi
    ERRORS+=("$f (exit $EXIT_CODE)")
    FAILED=$((FAILED + 1))
    # Clean up tmp file
    rm -f "$TMPFILE"
    continue
  fi

  # Check tmp file has content
  if [ ! -s "$TMPFILE" ]; then
    echo -e "    ${YELLOW}[!] webcrack produced empty output, keeping original${RESET}"
    ERRORS+=("$f (empty output)")
    FAILED=$((FAILED + 1))
    rm -f "$TMPFILE"
    continue
  fi

  # Replace original with deobfuscated version
  mv "$TMPFILE" "$f"
  SIZE=$(wc -c < "$f")
  echo -e "    ${GREEN}[✓] Done — ${SIZE} bytes${RESET}"
  SUCCESS=$((SUCCESS + 1))

done

# ── Summary ───────────────────────────────────
echo ""
echo -e "─────────────────────────────────────────"
echo -e "  ${GREEN}Success : $SUCCESS${RESET}"
echo -e "  ${RED}Failed  : $FAILED${RESET}"
echo -e "  ${YELLOW}Skipped : $SKIPPED${RESET}"
echo -e "  Total   : $TOTAL"
echo -e "─────────────────────────────────────────"

# Print error details if any
if [ ${#ERRORS[@]} -gt 0 ]; then
  echo ""
  echo -e "${RED}[!] Files that failed:${RESET}"
  for e in "${ERRORS[@]}"; do
    echo -e "    ${RED}• $e${RESET}"
  done
fi

echo ""