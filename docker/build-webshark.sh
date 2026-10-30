#!/bin/sh
# usage: build-webshark.sh <WebShark source checkout> <output directory>
# Builds only the WebShark UI for /webshark/; needs Node.js ^22.22.3, ^24.15.0 or >=26, npm, gzip, wget and python3.
set -eu

SRC=$(cd "$1" && pwd)
mkdir -p "$2"
OUT=$(cd "$2" && pwd)

cd "$SRC/ui"
npm ci --no-audit --no-fund
npm run build

cp -a dist/webshark/. "$OUT/"

# WebShark's own Dockerfile does the same, in case the build's base is "/"
sed -i 's|href="/"|href="/webshark/"|g' "$OUT/index.html"
sh "$SRC/scripts/vendor-offline-fonts.sh" "$OUT"

# Drop source maps and ship large files only as .gz: the server sends the .gz
# to browsers and decompresses it for any other client.
find "$OUT" -name '*.map' -delete
find "$OUT" -type f -size +16k \
  \( -name '*.wasm' -o -name '*.data' -o -name '*.js' -o -name '*.css' -o -name '*.json' -o -name '*.svg' \) \
  -exec gzip -9 -f {} \;

# WebShark is AGPL-3.0: ship its license and where its source is.
cp "$SRC/LICENSE" "$OUT/LICENSE"

{
  echo "WebShark, built from $(git -C "$SRC" remote get-url origin 2>/dev/null || echo https://github.com/QXIP/webshark)"
  echo "commit $(git -C "$SRC" rev-parse HEAD 2>/dev/null || echo unknown)"
  echo
  echo "WebShark is licensed under the GNU Affero General Public License v3.0 (see"
  echo "LICENSE). Its Wireshark WebAssembly build (Wiregasm) is licensed under the"
  echo "GNU General Public License v2.0. Their source is available at the"
  echo "repository and commit above."
} > "$OUT/NOTICE"
