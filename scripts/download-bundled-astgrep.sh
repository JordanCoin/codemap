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
  "darwin amd64 app-x86_64-apple-darwin.zip"
  "darwin arm64 app-aarch64-apple-darwin.zip"
  "linux amd64 app-x86_64-unknown-linux-gnu.zip"
  "linux arm64 app-aarch64-unknown-linux-gnu.zip"
  "windows amd64 app-x86_64-pc-windows-msvc.zip"
)

rm -rf "$output_dir"

for target in "${targets[@]}"; do
  read -r goos goarch asset <<<"$target"
  dest="$output_dir/${goos}_${goarch}"
  mkdir -p "$dest"

  python3 - "$version" "$asset" "$dest" <<'PY'
import io
import os
import shutil
import sys
import urllib.request
import zipfile

version, asset, dest = sys.argv[1:]
url = f"https://github.com/ast-grep/ast-grep/releases/download/{version}/{asset}"
print(f"Downloading {url}", file=sys.stderr)

with urllib.request.urlopen(url) as response:
    data = response.read()

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
