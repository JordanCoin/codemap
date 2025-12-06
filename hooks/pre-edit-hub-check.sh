#!/bin/bash
# codemap pre-edit hub check hook
# Warns before editing hub files (files imported by 3+ other files)

# Read the tool input from stdin
input=$(cat)

# Extract file_path from the Edit tool input
file_path=$(echo "$input" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

if [ -z "$file_path" ]; then
    exit 0
fi

# Get the project root (walk up to find .git)
dir=$(dirname "$file_path")
root="$dir"
while [ "$root" != "/" ] && [ ! -d "$root/.git" ]; do
    root=$(dirname "$root")
done

if [ ! -d "$root/.git" ]; then
    exit 0
fi

# Get relative path from root
rel_path="${file_path#$root/}"

# Run hub-check from the project root
cd "$root" || exit 0

# Try local hub-check first, then PATH
if [ -x "./hub-check" ]; then
    result=$(./hub-check "$rel_path" 2>/dev/null)
elif command -v hub-check &>/dev/null; then
    result=$(hub-check "$rel_path" 2>/dev/null)
else
    exit 0
fi

if [ -n "$result" ]; then
    echo ""
    echo "$result"
    echo ""
fi

exit 0
