#!/bin/bash
# codemap session-stop hook
# Summarizes what happened in the session

cd "$CLAUDE_PROJECT_DIR" 2>/dev/null || exit 0

echo ""
echo "📊 Session Summary"
echo "=================="

# Show files modified since session start (using git)
modified=$(git diff --name-only 2>/dev/null | head -10)
if [ -n "$modified" ]; then
    echo ""
    echo "Files modified:"
    for f in $modified; do
        if [ -x "./hub-check" ]; then
            hub_info=$(./hub-check "$f" 2>/dev/null | grep -o "HUB FILE\|Imported by [0-9]* files")
            if [ -n "$hub_info" ]; then
                echo "  ⚠️  $f ($hub_info)"
            else
                echo "  • $f"
            fi
        else
            echo "  • $f"
        fi
    done
fi

# Show new untracked files
untracked=$(git ls-files --others --exclude-standard 2>/dev/null | head -5)
if [ -n "$untracked" ]; then
    echo ""
    echo "New files created:"
    for f in $untracked; do
        echo "  + $f"
    done
fi

echo ""
