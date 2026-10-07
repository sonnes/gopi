#!/bin/sh
# Usage: ./build.sh [part.html ...]   -> writes index.html (all parts in order if none given)
# Usage: ./build.sh --shot part.html  -> writes shots/<part>.png (light) and shots/<part>-dark.png
set -e
cd "$(dirname "$0")"
ORDER="ink text swiss schematic annotated"
mkdir -p shots
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

shell() { # $1 out, $2 theme, rest parts
  out=$1; theme=$2; shift 2
  {
    cat <<HEAD
<!doctype html><html lang="en" data-theme="$theme"><head><meta charset="utf-8">
<title>gopi docs: figure styles</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600;700&family=Noto+Sans:wght@400;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="../../src/styles/tokens.css"><link rel="stylesheet" href="../diagrams/shared.css">
</head><body>
<header class="top"><div class="top-inner"><h1>gopi docs · figure styles</h1><nav>
HEAD
    for p in "$@"; do id=$(basename "$p" .html); echo "<a href=\"#$id\">$id</a>"; done
    cat <<'MID'
</nav><button onclick="const r=document.documentElement;r.dataset.theme=r.dataset.theme==='dark'?'light':'dark'">theme</button></div></header>
<main>
MID
    [ -f intro.html ] && [ $# -gt 1 ] && cat intro.html
    for p in "$@"; do cat "$p"; echo; done
    echo "</main></body></html>"
  } > "$out"
}

if [ "$1" = "--shot" ]; then
  p=$2; id=$(basename "$p" .html)
  shell "shot-$id.html" light "$p"
  shell "shot-$id-dark.html" dark "$p"
  H=${3:-6000}
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --virtual-time-budget=4000 --window-size=1280,$H --screenshot="$PWD/shots/$id.png" "file://$PWD/shot-$id.html" 2>/dev/null
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --virtual-time-budget=4000 --window-size=1280,$H --screenshot="$PWD/shots/$id-dark.png" "file://$PWD/shot-$id-dark.html" 2>/dev/null
  echo "shots/$id.png shots/$id-dark.png"
  exit 0
fi

if [ $# -eq 0 ]; then
  set --; for id in $ORDER; do [ -f "parts/$id.html" ] && set -- "$@" "parts/$id.html"; done
fi
shell index.html light "$@"
echo index.html
