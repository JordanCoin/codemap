#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: download-bundled-astgrep.sh [--version <version>] [--output-dir <dir>]

Downloads pinned ast-grep release assets and extracts ast-grep/sg into:
  <output-dir>/<goos>_<goarch>/

Environment:
  AST_GREP_VERSION   Override the pinned ast-grep release version.
EOF
}

version="${AST_GREP_VERSION:-0.42.1}"
output_dir="bundled-tools"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --output-dir)
      output_dir="${2:-}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to download bundled ast-grep archives" >&2
  exit 1
fi

targets=(
  "darwin amd64 app-x86_64-apple-darwin.zip a038965bfd7fe44257c771cdf8918dc3467dd8ec0eef673b8b14f639b144cdbd"
  "darwin arm64 app-aarch64-apple-darwin.zip c3961d8e8a4ee0ce2d0d98c7beeb168bb331cdc766b53630118a7b6c4fd39015"
  "linux amd64 app-x86_64-unknown-linux-gnu.zip 5de8b87cba67fc8dc3e239d54b6484802ad745a7ae3de76be4fe89661dc52657"
  "linux arm64 app-aarch64-unknown-linux-gnu.zip 3ba383839044cf9817929435f5ce0027f91d06931e8efb32d942e58d73d92be5"
  "windows amd64 app-x86_64-pc-windows-msvc.zip fe34f631bb24c08ad146f92ca2a92971a53d179461b509fd8d32dc863bff9f83"
)

case "$output_dir" in
  ""|"/"|".")
    echo "Refusing to use unsafe output directory: $output_dir" >&2
    exit 1
    ;;
esac

for target in "${targets[@]}"; do
  read -r goos goarch asset asset_sha256 <<<"$target"
  dest="$output_dir/${goos}_${goarch}"
  rm -rf "$dest"
  mkdir -p "$dest"

  python3 - "$version" "$asset" "$asset_sha256" "$dest" <<'PY'
import hashlib
import io
import os
import shutil
import sys
import time
import urllib.error
import urllib.request
import zipfile

DOWNLOAD_TIMEOUT_SECONDS = 30
DOWNLOAD_RETRIES = 3
RETRY_BACKOFF_SECONDS = 2

version, asset, expected_sha256, dest = sys.argv[1:]
url = f"https://github.com/ast-grep/ast-grep/releases/download/{version}/{asset}"
print(f"Downloading {url}", file=sys.stderr)

last_error = None
for attempt in range(1, DOWNLOAD_RETRIES + 1):
    try:
        with urllib.request.urlopen(url, timeout=DOWNLOAD_TIMEOUT_SECONDS) as response:
            data = response.read()
        break
    except (TimeoutError, urllib.error.URLError, OSError) as exc:
        last_error = exc
        if attempt == DOWNLOAD_RETRIES:
            raise
        print(
            f"Download attempt {attempt} failed for {url}: {exc}. "
            f"Retrying in {RETRY_BACKOFF_SECONDS}s...",
            file=sys.stderr,
        )
        time.sleep(RETRY_BACKOFF_SECONDS)
else:
    raise last_error

actual_sha256 = hashlib.sha256(data).hexdigest()
if actual_sha256 != expected_sha256:
    raise SystemExit(
        f"{asset} sha256 mismatch: expected {expected_sha256}, got {actual_sha256}"
    )

required = ["ast-grep", "sg"]
if asset.endswith("windows-msvc.zip"):
    required = [name + ".exe" for name in required]

with zipfile.ZipFile(io.BytesIO(data)) as archive:
    names = set(archive.namelist())
    missing = [name for name in required if name not in names]
    if missing:
        raise SystemExit(
            f"{asset} did not contain required files: {', '.join(missing)}"
        )

    for name in required:
        target = os.path.join(dest, name)
        with archive.open(name) as src, open(target, "wb") as dst:
            shutil.copyfileobj(src, dst)
        os.chmod(target, 0o755)
PY
done

printf 'Bundled ast-grep %s into %s\n' "$version" "$output_dir"
