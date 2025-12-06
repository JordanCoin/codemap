#!/bin/bash
# codemap session-start hook
# Runs automatically when Claude Code starts a new session

cd "$CLAUDE_PROJECT_DIR" 2>/dev/null || cd "$(pwd)"

# Show project structure with hub warnings
if [ -x "./codemap-test" ]; then
    echo "📍 Project Context:"
    echo ""
    ./codemap-test . 2>/dev/null | head -40
    echo ""

    # Show hub files
    if [ -x "./hub-check" ]; then
        echo "⚠️  High-impact files (hubs):"
        for f in $(find . -name "*.go" -not -path "./vendor/*" | head -20); do
            result=$(./hub-check "${f#./}" 2>/dev/null | grep "HUB FILE")
            if [ -n "$result" ]; then
                echo "   $result"
            fi
        done
    fi
elif command -v codemap &>/dev/null; then
    echo "📍 Project Context:"
    codemap . 2>/dev/null | head -40
fi
