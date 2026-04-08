#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: codemap-blast-radius.sh [--json|--markdown|--text] [--ref <base-ref>] [root]

Build a compact codemap review bundle from:
  1. codemap --diff
  2. codemap --deps --diff
  3. codemap --importers for each changed file

Examples:
  bash scripts/codemap-blast-radius.sh --markdown --ref main .
  bash scripts/codemap-blast-radius.sh --json --ref develop /path/to/repo
EOF
}

format="markdown"
ref="main"
root="."
max_total_chars="${CODEMAP_BLAST_MAX_TOTAL_CHARS:-24000}"
max_changed_files="${CODEMAP_BLAST_MAX_CHANGED_FILES:-20}"
max_affected="${CODEMAP_BLAST_MAX_AFFECTED:-12}"
max_context="${CODEMAP_BLAST_MAX_CONTEXT:-8}"
max_snippets="${CODEMAP_BLAST_MAX_SNIPPETS:-8}"
max_snippets_per_changed="${CODEMAP_BLAST_MAX_SNIPPETS_PER_CHANGED:-2}"
snippet_radius="${CODEMAP_BLAST_SNIPPET_RADIUS:-2}"
max_snippet_chars="${CODEMAP_BLAST_MAX_SNIPPET_CHARS:-700}"
max_diff_chars="${CODEMAP_BLAST_MAX_DIFF_CHARS:-8000}"
max_deps_chars="${CODEMAP_BLAST_MAX_DEPS_CHARS:-5000}"
max_importers_chars="${CODEMAP_BLAST_MAX_IMPORTERS_CHARS:-6000}"
max_importer_files="${CODEMAP_BLAST_MAX_IMPORTER_FILES:-8}"
max_importers_per_file="${CODEMAP_BLAST_MAX_IMPORTERS_PER_FILE:-12}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json)
      format="json"
      shift
      ;;
    --markdown|--md)
      format="markdown"
      shift
      ;;
    --text)
      format="text"
      shift
      ;;
    --ref)
      ref="${2:-}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      root="$1"
      shift
      ;;
  esac
done

if ! command -v codemap >/dev/null 2>&1; then
  echo "codemap is required on PATH" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required on PATH" >&2
  exit 1
fi

strip_ansi() {
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import re, sys; sys.stdout.write(re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", sys.stdin.read()))'
  else
    cat
  fi
}

render_codemap() {
  codemap "$@" | strip_ansi
}

truncate_chars() {
  local text="$1"
  local max_chars="$2"
  local label="$3"

  if (( max_chars <= 0 )); then
    printf '... [%s omitted]\n' "$label"
    return
  fi

  if ((${#text} <= max_chars)); then
    printf '%s' "$text"
    return
  fi

  local marker
  marker=$'\n... ['"$label"' truncated to '"$max_chars"$' chars]\n'
  local keep_chars=$((max_chars - ${#marker}))
  if (( keep_chars < 0 )); then
    keep_chars=0
  fi
  printf '%s%s' "${text:0:keep_chars}" "$marker"
}

capture_codemap_block() {
  local max_chars="$1"
  local label="$2"
  shift 2
  local text
  text="$(render_codemap "$@" || true)"
  truncate_chars "$text" "$max_chars" "$label"
}

min_int() {
  if (( $1 < $2 )); then
    printf '%s' "$1"
  else
    printf '%s' "$2"
  fi
}

abs_root="$(cd "$root" && pwd)"
diff_json="$(codemap --json --diff --ref "$ref" "$abs_root")"
deps_json="$(codemap --json --deps --diff --ref "$ref" "$abs_root")"

all_changed_files=()
while IFS= read -r file; do
  all_changed_files+=("$file")
done < <(printf '%s' "$diff_json" | jq -r '.files[].path')

changed_files=("${all_changed_files[@]:0:max_changed_files}")

importers_json='[]'
for file in "${changed_files[@]}"; do
  [[ -n "$file" ]] || continue
  report="$(codemap --json --importers "$file" "$abs_root")"
  report="$(jq -c \
    --argjson max "$max_importers_per_file" '
    .importers_total = .importer_count
    | .imports_total = ((.imports // []) | length)
    | .hub_imports_total = ((.hub_imports // []) | length)
    | .importers = ((.importers // [])[:$max])
    | .imports = ((.imports // [])[:$max])
    | .hub_imports = ((.hub_imports // [])[:$max])
  ' <<<"$report")"
  importers_json="$(jq -c --argjson report "$report" '. + [$report]' <<<"$importers_json")"
done

diff_json_capped="$(jq -c \
  --argjson max "$max_changed_files" '
  .changed_files_total = (.files | length)
  | .files = (.files[:$max])
  | .impact = ((.impact // [])[:$max])
' <<<"$diff_json")"

deps_json_capped="$(jq -c \
  --argjson max "$max_changed_files" '
  .changed_files_total = (.files | length)
  | .files = (.files[:$max])
' <<<"$deps_json")"

raw_impacted_json="$(jq -cn \
  --argjson diff "$diff_json" \
  --argjson importers "$importers_json" '
  def changed_paths: ($diff.files | map(.path));
  [
    $importers[] as $report
    | $report.importers[]?
    | . as $path
    | select((changed_paths | index($path)) | not)
    | {
        path: $path,
        via: $report.file,
        relation: "imports_changed_file",
        via_is_hub: $report.is_hub,
        via_importer_count: $report.importer_count
      }
  ]
  | unique_by(.path + "|" + .via)
')"

impacted_json="$(jq -c \
  --argjson max "$max_affected" '
  sort_by(-(.via_importer_count // 0), .path, .via)
  | .[:$max]
' <<<"$raw_impacted_json")"

raw_context_json="$(jq -cn \
  --argjson diff "$diff_json" \
  --argjson importers "$importers_json" '
  def changed_paths: ($diff.files | map(.path));
  [
    $importers[] as $report
    | $report.imports[]?
    | . as $path
    | select((changed_paths | index($path)) | not)
    | {
        path: $path,
        via: $report.file,
        relation: (if (($report.hub_imports // []) | index($path)) != null then "shared_hub_dependency" else "internal_dependency" end),
        is_hub: ((($report.hub_imports // []) | index($path)) != null)
      }
  ]
  | unique_by(.path + "|" + .via)
')"

context_json="$(jq -c \
  --argjson max "$max_context" '
  sort_by((if .relation == "shared_hub_dependency" then 0 else 1 end), .path, .via)
  | .[:$max]
' <<<"$raw_context_json")"

summary_json="$(jq -cn \
  --argjson diff "$diff_json" \
  --argjson importers "$importers_json" \
  --argjson raw_impacted "$raw_impacted_json" \
  --argjson impacted "$impacted_json" \
  --argjson raw_context "$raw_context_json" \
  --argjson context "$context_json" '
  {
    changed_files: ($diff.files | length),
    changed_files_total: ($diff.changed_files_total // ($diff.files | length)),
    files_with_dependents: ([ $importers[] | select(.importer_count > 0) ] | length),
    impacted_outside_diff_total: ($raw_impacted | map(.path) | unique | length),
    impacted_outside_diff_shown: ($impacted | map(.path) | unique | length),
    dependency_context_outside_diff_total: ($raw_context | map(.path) | unique | length),
    dependency_context_outside_diff_shown: ($context | map(.path) | unique | length),
    max_direct_dependents: (([$importers[] | .importer_count] | max) // 0),
    highest_blast_radius: (
      [ $importers[] | select(.importer_count > 0) ]
      | sort_by(-.importer_count, .file)
      | .[0] // null
    )
  }
')"

snippets_json='[]'
if command -v python3 >/dev/null 2>&1; then
  snippets_json="$(
    jq -n \
      --arg root "$abs_root" \
      --argjson diff "$diff_json" \
      --argjson deps "$deps_json" \
      --argjson impacted "$impacted_json" \
      --argjson context "$context_json" \
      --argjson max_snippets "$max_snippets" \
      --argjson max_snippets_per_changed "$max_snippets_per_changed" \
      --argjson snippet_radius "$snippet_radius" \
      --argjson max_snippet_chars "$max_snippet_chars" \
      '{
        root: $root,
        diff: $diff,
        deps: $deps,
        impacted: $impacted,
        context: $context,
        max_snippets: $max_snippets,
        max_snippets_per_changed: $max_snippets_per_changed,
        snippet_radius: $snippet_radius,
        max_snippet_chars: $max_snippet_chars,
        max_changed_files: '"$max_changed_files"',
        max_importers_per_file: '"$max_importers_per_file"'
      }' \
      | python3 -c '
import json
import pathlib
import re
import sys

payload = json.load(sys.stdin)
root = pathlib.Path(payload["root"])
diff_files = payload["diff"].get("files", [])
deps_files = payload["deps"].get("files", [])
impacted = payload.get("impacted", [])
context = payload.get("context", [])
max_snippets = int(payload.get("max_snippets", 8))
max_snippets_per_changed = int(payload.get("max_snippets_per_changed", 2))
snippet_radius = int(payload.get("snippet_radius", 2))
max_snippet_chars = int(payload.get("max_snippet_chars", 700))

lang_map = {
    ".go": "go",
    ".py": "python",
    ".js": "javascript",
    ".jsx": "javascript",
    ".ts": "typescript",
    ".tsx": "typescript",
    ".swift": "swift",
    ".kt": "kotlin",
    ".kts": "kotlin",
    ".java": "java",
    ".rb": "ruby",
    ".rs": "rust",
    ".sh": "bash",
}

changed_meta = {}
for item in diff_files:
    path = item.get("path", "")
    pure = pathlib.PurePosixPath(path)
    changed_meta[path] = {
        "functions": [],
        "stem": pure.stem,
        "dir": str(pure.parent) if str(pure.parent) != "." else "",
        "dir_base": pure.parent.name if str(pure.parent) != "." else "",
        "path_no_ext": str(pure.with_suffix("")),
    }

for item in deps_files:
    path = item.get("path", "")
    pure = pathlib.PurePosixPath(path)
    changed_meta[path] = {
        "functions": item.get("functions", []),
        "stem": pure.stem,
        "dir": str(pure.parent) if str(pure.parent) != "." else "",
        "dir_base": pure.parent.name if str(pure.parent) != "." else "",
        "path_no_ext": str(pure.with_suffix("")),
    }

def unique_terms(via):
    meta = changed_meta.get(via, {})
    terms = []
    seen = set()
    for fn in sorted(meta.get("functions", []), key=lambda v: (-len(v), v)):
        if fn and fn not in seen:
            terms.append((fn, "symbol"))
            seen.add(fn)
    for value, kind in [
        (meta.get("path_no_ext", ""), "path"),
        (meta.get("dir", ""), "path"),
        (meta.get("dir_base", ""), "identifier"),
        (meta.get("stem", ""), "identifier"),
    ]:
        if value and value not in seen:
            terms.append((value, kind))
            seen.add(value)
    return terms

def make_excerpt(lines, index):
    start = max(0, index - snippet_radius)
    end = min(len(lines), index + snippet_radius + 1)
    excerpt = []
    for lineno in range(start, end):
        excerpt.append(f"{lineno + 1:4d} | {lines[lineno]}")
    text = "\n".join(excerpt)
    if len(text) > max_snippet_chars:
        text = text[:max_snippet_chars].rstrip() + "\n... [truncated]"
    return text

def find_snippet(target_path, via, category, reason):
    abs_path = root / target_path
    if not abs_path.is_file():
        return None

    try:
        content = abs_path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        content = abs_path.read_text(encoding="utf-8", errors="replace")

    lines = content.splitlines()
    if not lines:
        return None

    terms = unique_terms(via)
    for term, kind in terms:
        if kind == "symbol":
            pattern = re.compile(r"\b" + re.escape(term) + r"\b")
            for idx, line in enumerate(lines):
                if pattern.search(line):
                    return {
                        "category": category,
                        "path": target_path,
                        "via": via,
                        "reason": reason,
                        "matched_term": term,
                        "match_kind": kind,
                        "language": lang_map.get(pathlib.PurePosixPath(target_path).suffix, "text"),
                        "excerpt": make_excerpt(lines, idx),
                    }
        else:
            for idx, line in enumerate(lines):
                if term in line:
                    return {
                        "category": category,
                        "path": target_path,
                        "via": via,
                        "reason": reason,
                        "matched_term": term,
                        "match_kind": kind,
                        "language": lang_map.get(pathlib.PurePosixPath(target_path).suffix, "text"),
                        "excerpt": make_excerpt(lines, idx),
                    }
    return None

snippets = []
per_via_counts = {}
for item in impacted:
    if len(snippets) >= max_snippets:
        break
    if per_via_counts.get(item["via"], 0) >= max_snippets_per_changed:
        continue
    via = item["via"]
    snippet = find_snippet(
        item["path"],
        via,
        "impacted_outside_diff",
        f"depends on changed file {via}",
    )
    if snippet:
        snippets.append(snippet)
        per_via_counts[via] = per_via_counts.get(via, 0) + 1

for item in context:
    if len(snippets) >= max_snippets:
        break
    if per_via_counts.get(item["via"], 0) >= max_snippets_per_changed:
        continue
    via = item["via"]
    snippet = find_snippet(
        item["path"],
        via,
        "dependency_context_outside_diff",
        f"reachable from changed file {via}",
    )
    if snippet:
        snippets.append(snippet)
        per_via_counts[via] = per_via_counts.get(via, 0) + 1

json.dump(snippets, sys.stdout)
'
  )"
fi

if [[ "$format" == "json" ]]; then
  diff_text="$(capture_codemap_block "$max_diff_chars" "diff" --diff --ref "$ref" "$abs_root")"
  deps_text="$(capture_codemap_block "$max_deps_chars" "deps" --deps --diff --ref "$ref" "$abs_root")"
  importers_rendered='[]'
  importer_budget="$max_importers_chars"
  importer_count=0
  for file in "${changed_files[@]}"; do
    [[ -n "$file" ]] || continue
    if (( importer_count >= max_importer_files )); then
      break
    fi
    if (( importer_budget <= 0 )); then
      break
    fi
    per_file_budget="$(min_int "$importer_budget" 1200)"
    text="$(capture_codemap_block "$per_file_budget" "importers:$file" --importers "$file" "$abs_root")"
    importers_rendered="$(jq -c --arg file "$file" --arg text "$text" '. + [{file: $file, text: $text}]' <<<"$importers_rendered")"
    importer_budget=$((importer_budget - ${#text}))
    importer_count=$((importer_count + 1))
  done

  jq -n \
    --arg root "$abs_root" \
    --arg ref "$ref" \
    --argjson diff "$diff_json_capped" \
    --argjson deps "$deps_json_capped" \
    --argjson importers "$importers_json" \
    --argjson summary "$summary_json" \
    --argjson impacted "$impacted_json" \
    --argjson context "$context_json" \
    --argjson snippets "$snippets_json" \
    --argjson max_affected "$max_affected" \
    --argjson max_context "$max_context" \
    --argjson max_snippets "$max_snippets" \
    --argjson max_snippets_per_changed "$max_snippets_per_changed" \
    --argjson snippet_radius "$snippet_radius" \
    --argjson max_snippet_chars "$max_snippet_chars" \
    --argjson max_total_chars "$max_total_chars" \
    --argjson max_diff_chars "$max_diff_chars" \
    --argjson max_deps_chars "$max_deps_chars" \
    --argjson max_importers_chars "$max_importers_chars" \
    --argjson max_changed_files "$max_changed_files" \
    --argjson max_importer_files "$max_importer_files" \
    --argjson max_importers_per_file "$max_importers_per_file" \
    --arg diff_text "$diff_text" \
    --arg deps_text "$deps_text" \
    --argjson importers_rendered "$importers_rendered" \
    '{
      root: $root,
      ref: $ref,
      summary: $summary,
      diff: $diff,
      deps: $deps,
      importers: $importers,
      limits: {
        max_affected: $max_affected,
        max_context: $max_context,
        max_snippets: $max_snippets,
        max_snippets_per_changed: $max_snippets_per_changed,
        snippet_radius: $snippet_radius,
        max_snippet_chars: $max_snippet_chars,
        max_total_chars: $max_total_chars,
        max_diff_chars: $max_diff_chars,
        max_deps_chars: $max_deps_chars,
        max_importers_chars: $max_importers_chars,
        max_changed_files: $max_changed_files,
        max_importer_files: $max_importer_files,
        max_importers_per_file: $max_importers_per_file
      },
      impacted_outside_diff: $impacted,
      dependency_context_outside_diff: $context,
      snippets: $snippets,
      rendered: {
        diff: $diff_text,
        deps: $deps_text,
        importers: $importers_rendered
      }
    }'
  exit 0
fi

output=""
remaining_chars="$max_total_chars"

append_block() {
  local text="$1"
  local label="$2"
  if (( remaining_chars <= 0 )); then
    return 1
  fi
  if ((${#text} <= remaining_chars)); then
    output+="$text"
    remaining_chars=$((remaining_chars - ${#text}))
    return 0
  fi
  local marker
  marker=$'\n... ['"$label"' omitted after total budget '"$max_total_chars"$' chars]\n'
  local keep_chars=$((remaining_chars - ${#marker}))
  if (( keep_chars < 0 )); then
    keep_chars=0
  fi
  output+="${text:0:keep_chars}${marker}"
  remaining_chars=0
  return 1
}

if [[ "$format" == "markdown" ]]; then
  summary_block="# Codemap Blast Radius"$'\n\n'
  summary_block+="- Root: \`$abs_root\`"$'\n'
  summary_block+="- Base ref: \`$ref\`"$'\n\n'
  summary_block+="## Summary"$'\n\n'
  summary_block+="- Changed files: $(jq -r '.changed_files' <<<"$summary_json") shown of $(jq -r '.changed_files_total' <<<"$summary_json")"$'\n'
  summary_block+="- Changed files with direct dependents: $(jq -r '.files_with_dependents' <<<"$summary_json")"$'\n'
  summary_block+="- Affected files outside diff: $(jq -r '.impacted_outside_diff_shown' <<<"$summary_json") shown of $(jq -r '.impacted_outside_diff_total' <<<"$summary_json")"$'\n'
  summary_block+="- Dependency context outside diff: $(jq -r '.dependency_context_outside_diff_shown' <<<"$summary_json") shown of $(jq -r '.dependency_context_outside_diff_total' <<<"$summary_json")"$'\n'
  if [[ "$(jq -r '.highest_blast_radius != null' <<<"$summary_json")" == "true" ]]; then
    summary_block+="- Highest blast radius: \`$(jq -r '.highest_blast_radius.file' <<<"$summary_json")\` ($(jq -r '.highest_blast_radius.importer_count' <<<"$summary_json") direct dependents)"$'\n'
  fi
  summary_block+="- Output budgets: total ${max_total_chars} chars, diff ${max_diff_chars}, deps ${max_deps_chars}, importers ${max_importers_chars}"$'\n'
  summary_block+="- Snippet limits: ${max_snippets} total, ${max_snippets_per_changed} per changed file, ${max_snippet_chars} chars max"$'\n\n'
  append_block "$summary_block" "summary" || { printf '%s' "$output"; exit 0; }

  if [[ "$(jq 'length' <<<"$impacted_json")" -gt 0 ]]; then
    affected_block="## Affected Outside Diff"$'\n\n'
    affected_block+="$(jq -r '.[] | "- `\(.path)` depends on changed file `\(.via)`\((if .via_is_hub then " [hub, \(.via_importer_count) dependents]" else "" end))"' <<<"$impacted_json")"$'\n\n'
    append_block "$affected_block" "affected outside diff" || { printf '%s' "$output"; exit 0; }
  fi

  if [[ "$(jq 'length' <<<"$context_json")" -gt 0 ]]; then
    context_block="## Dependency Context Outside Diff"$'\n\n'
    context_block+="$(jq -r '.[] | "- changed file `\(.via)` reaches `\(.path)`\((if .relation == "shared_hub_dependency" then " [shared hub]" else "" end))"' <<<"$context_json")"$'\n\n'
    append_block "$context_block" "dependency context" || { printf '%s' "$output"; exit 0; }
  fi

  if [[ "$(jq 'length' <<<"$snippets_json")" -gt 0 ]]; then
    snippets_block="## Impact Snippets"$'\n'
    while IFS= read -r snippet; do
      [[ -n "$snippet" ]] || continue
      path="$(jq -r '.path' <<<"$snippet")"
      via="$(jq -r '.via' <<<"$snippet")"
      reason="$(jq -r '.reason' <<<"$snippet")"
      matched_term="$(jq -r '.matched_term' <<<"$snippet")"
      match_kind="$(jq -r '.match_kind' <<<"$snippet")"
      language="$(jq -r '.language' <<<"$snippet")"
      excerpt="$(jq -r '.excerpt' <<<"$snippet")"
      snippets_block+=$'\n'"### \`$path\` via \`$via\`"$'\n\n'
      snippets_block+="- Reason: $reason"$'\n'
      snippets_block+="- Match: \`$matched_term\` ($match_kind)"$'\n\n'
      snippets_block+="\`\`\`$language"$'\n'"$excerpt"$'\n'"\`\`\`"$'\n'
    done < <(jq -c '.[]' <<<"$snippets_json")
    snippets_block+=$'\n'
    append_block "$snippets_block" "impact snippets" || { printf '%s' "$output"; exit 0; }
  fi

  diff_block="## Diff"$'\n\n```text\n'
  diff_block+="$(capture_codemap_block "$max_diff_chars" "diff" --diff --ref "$ref" "$abs_root")"
  diff_block+=$'\n```\n\n'
  append_block "$diff_block" "diff section" || { printf '%s' "$output"; exit 0; }

  deps_block="## Dependency Flow (Changed Files)"$'\n\n```text\n'
  deps_block+="$(capture_codemap_block "$max_deps_chars" "deps" --deps --diff --ref "$ref" "$abs_root")"
  deps_block+=$'\n```\n'
  append_block "$deps_block" "deps section" || { printf '%s' "$output"; exit 0; }

  if ((${#changed_files[@]} > 0)); then
    importers_block=$'\n## Importers\n'
    importer_budget="$max_importers_chars"
    importer_count=0
    for file in "${changed_files[@]}"; do
      [[ -n "$file" ]] || continue
      if (( importer_count >= max_importer_files )); then
        importers_block+=$'\n... [additional importer sections omitted]\n'
        break
      fi
      if (( importer_budget <= 0 )); then
        importers_block+=$'\n... [importer budget exhausted]\n'
        break
      fi
      per_file_budget="$(min_int "$importer_budget" 1200)"
      text="$(capture_codemap_block "$per_file_budget" "importers:$file" --importers "$file" "$abs_root")"
      importers_block+=$'\n'"### \`$file\`"$'\n\n```text\n'"$text"$'\n```\n'
      importer_budget=$((importer_budget - ${#text}))
      importer_count=$((importer_count + 1))
    done
    append_block "$importers_block" "importers section" || { printf '%s' "$output"; exit 0; }
  fi

  printf '%s' "$output"
  exit 0
fi

summary_block="CODEMAP BLAST RADIUS"$'\n'
summary_block+="root=$abs_root"$'\n'
summary_block+="ref=$ref"$'\n\n'
summary_block+="[summary]"$'\n'
summary_block+="changed_files=$(jq -r '.changed_files' <<<"$summary_json")/$(jq -r '.changed_files_total' <<<"$summary_json")"$'\n'
summary_block+="files_with_dependents=$(jq -r '.files_with_dependents' <<<"$summary_json")"$'\n'
summary_block+="impacted_outside_diff=$(jq -r '.impacted_outside_diff_shown' <<<"$summary_json")/$(jq -r '.impacted_outside_diff_total' <<<"$summary_json")"$'\n'
summary_block+="dependency_context_outside_diff=$(jq -r '.dependency_context_outside_diff_shown' <<<"$summary_json")/$(jq -r '.dependency_context_outside_diff_total' <<<"$summary_json")"$'\n'
summary_block+="output_budgets=${max_total_chars}_total,${max_diff_chars}_diff,${max_deps_chars}_deps,${max_importers_chars}_importers"$'\n'
summary_block+="snippet_limits=${max_snippets}_total,${max_snippets_per_changed}_per_changed,${max_snippet_chars}_chars"$'\n\n'
append_block "$summary_block" "summary" || { printf '%s' "$output"; exit 0; }

if [[ "$(jq 'length' <<<"$impacted_json")" -gt 0 ]]; then
  affected_block='[affected_outside_diff]'$'\n'
  affected_block+="$(jq -r '.[] | "\(.path) <= \(.via)"' <<<"$impacted_json")"$'\n\n'
  append_block "$affected_block" "affected outside diff" || { printf '%s' "$output"; exit 0; }
fi

if [[ "$(jq 'length' <<<"$context_json")" -gt 0 ]]; then
  context_block='[dependency_context_outside_diff]'$'\n'
  context_block+="$(jq -r '.[] | "\(.via) => \(.path)\((if .relation == "shared_hub_dependency" then " [shared hub]" else "" end))"' <<<"$context_json")"$'\n\n'
  append_block "$context_block" "dependency context" || { printf '%s' "$output"; exit 0; }
fi

if [[ "$(jq 'length' <<<"$snippets_json")" -gt 0 ]]; then
  snippets_block='[impact_snippets]'$'\n'
  while IFS= read -r snippet; do
    [[ -n "$snippet" ]] || continue
    path="$(jq -r '.path' <<<"$snippet")"
    via="$(jq -r '.via' <<<"$snippet")"
    matched_term="$(jq -r '.matched_term' <<<"$snippet")"
    excerpt="$(jq -r '.excerpt' <<<"$snippet")"
    snippets_block+=$'\n'"$path <= $via [$matched_term]"$'\n'"$excerpt"$'\n'
  done < <(jq -c '.[]' <<<"$snippets_json")
  snippets_block+=$'\n'
  append_block "$snippets_block" "impact snippets" || { printf '%s' "$output"; exit 0; }
fi

diff_block='[diff]'$'\n'
diff_block+="$(capture_codemap_block "$max_diff_chars" "diff" --diff --ref "$ref" "$abs_root")"$'\n'
append_block "$diff_block" "diff section" || { printf '%s' "$output"; exit 0; }

deps_block='[deps]'$'\n'
deps_block+="$(capture_codemap_block "$max_deps_chars" "deps" --deps --diff --ref "$ref" "$abs_root")"$'\n'
append_block "$deps_block" "deps section" || { printf '%s' "$output"; exit 0; }

if ((${#changed_files[@]} > 0)); then
  importers_block=""
  importer_budget="$max_importers_chars"
  importer_count=0
  for file in "${changed_files[@]}"; do
    [[ -n "$file" ]] || continue
    if (( importer_count >= max_importer_files )); then
      importers_block+=$'\n... [additional importer sections omitted]\n'
      break
    fi
    if (( importer_budget <= 0 )); then
      importers_block+=$'\n... [importer budget exhausted]\n'
      break
    fi
    per_file_budget="$(min_int "$importer_budget" 1200)"
    text="$(capture_codemap_block "$per_file_budget" "importers:$file" --importers "$file" "$abs_root")"
    importers_block+=$'\n'"[importers] $file"$'\n'"$text"$'\n'
    importer_budget=$((importer_budget - ${#text}))
    importer_count=$((importer_count + 1))
  done
  append_block "$importers_block" "importers section" || { printf '%s' "$output"; exit 0; }
fi

printf '%s' "$output"
