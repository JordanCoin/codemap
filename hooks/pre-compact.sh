#!/bin/bash
# codemap pre-compact hook
# Saves file graph state before context is compacted

cd "$CLAUDE_PROJECT_DIR" 2>/dev/null || exit 0

# Save current hub state to .codemap/ for recovery after compact
mkdir -p .codemap

if [ -x "./hub-check" ]; then
    echo "# Hub files at $(date)" > .codemap/hubs.txt
    for f in $(find . -name "*.go" -not -path "./vendor/*" -not -path "./.git/*" 2>/dev/null | head -50); do
        result=$(./hub-check "${f#./}" 2>/dev/null | grep "HUB FILE")
        if [ -n "$result" ]; then
            echo "${f#./}" >> .codemap/hubs.txt
        fi
    done

    echo ""
    echo "💾 Saved hub state to .codemap/hubs.txt before compact"
    echo "   ($(wc -l < .codemap/hubs.txt | tr -d ' ') hub files tracked)"
    echo ""
fi
