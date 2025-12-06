#!/bin/bash
# codemap prompt-submit hook
# Detects file mentions in user prompt and injects context

input=$(cat)
prompt=$(echo "$input" | grep -o '"prompt"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"prompt"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

if [ -z "$prompt" ]; then
    exit 0
fi

cd "$CLAUDE_PROJECT_DIR" 2>/dev/null || exit 0

# Look for file patterns in the prompt (e.g., "walker.go", "scanner/", ".go files")
# Extract potential file references
files_mentioned=""

# Check for .go, .ts, .py etc file mentions
for ext in go ts js py rs rb; do
    matches=$(echo "$prompt" | grep -oE '[a-zA-Z0-9_/-]+\.'$ext | head -3)
    if [ -n "$matches" ]; then
        files_mentioned="$files_mentioned $matches"
    fi
done

# Check for directory mentions like "scanner/" or "in the render package"
dirs=$(echo "$prompt" | grep -oE '(in |the )?[a-zA-Z0-9_]+(/| package| directory| folder)' | sed 's/ package//;s/ directory//;s/ folder//;s/in //;s/the //' | head -3)

if [ -n "$files_mentioned" ] || [ -n "$dirs" ]; then
    echo ""
    echo "📍 Context for mentioned files:"

    for f in $files_mentioned; do
        if [ -x "./hub-check" ]; then
            result=$(./hub-check "$f" 2>/dev/null)
            if [ -n "$result" ]; then
                echo "$result"
            fi
        fi
    done
    echo ""
fi
