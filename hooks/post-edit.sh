#!/bin/bash
# codemap post-edit hook
# Shows impact after editing a file

input=$(cat)
file_path=$(echo "$input" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

if [ -z "$file_path" ]; then
    exit 0
fi

# Get project root
dir=$(dirname "$file_path")
root="$dir"
while [ "$root" != "/" ] && [ ! -d "$root/.git" ]; do
    root=$(dirname "$root")
done
[ ! -d "$root/.git" ] && exit 0

rel_path="${file_path#$root/}"
cd "$root" || exit 0

# Check if this file is imported by others
if [ -x "./hub-check" ]; then
    importers=$(./hub-check "$rel_path" 2>/dev/null | grep "Imported by")
    if [ -n "$importers" ]; then
        echo ""
        echo "📢 Impact: $rel_path was modified"
        echo "   $importers"
        echo ""
    fi
fi
