#!/bin/bash
# Watch Mode Test Harness
# Generates code, runs watcher, captures output for review

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CODEMAP_BIN="${SCRIPT_DIR}/../../codemap-test"
TEST_PROJECT="/tmp/codemap-watch-test"
OUTPUT_DIR="${SCRIPT_DIR}/output/$(date +%Y%m%d-%H%M%S)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${BLUE}[test]${NC} $1"; }
success() { echo -e "${GREEN}[pass]${NC} $1"; }
warn() { echo -e "${YELLOW}[warn]${NC} $1"; }

# Setup
setup_test_project() {
    log "Creating test project at $TEST_PROJECT"
    rm -rf "$TEST_PROJECT"
    mkdir -p "$TEST_PROJECT"
    cd "$TEST_PROJECT"

    # Initialize git
    git init -q
    git config user.email "test@test.com"
    git config user.name "Test"

    # Create initial file structure
    mkdir -p src/{auth,api,utils}
    mkdir -p tests

    # Create initial files
    cat > src/main.go << 'EOF'
package main

import (
    "fmt"
    "myapp/auth"
    "myapp/api"
)

func main() {
    fmt.Println("Starting app...")
    auth.Init()
    api.Start()
}
EOF

    cat > src/auth/auth.go << 'EOF'
package auth

import "fmt"

func Init() {
    fmt.Println("Auth initialized")
}

func Login(user, pass string) bool {
    return user == "admin" && pass == "secret"
}

func Logout() {
    fmt.Println("Logged out")
}
EOF

    cat > src/api/server.go << 'EOF'
package api

import "fmt"

func Start() {
    fmt.Println("API server starting on :8080")
}

func handleRequest(path string) string {
    return "OK: " + path
}
EOF

    cat > src/utils/helpers.go << 'EOF'
package utils

func Max(a, b int) int {
    if a > b {
        return a
    }
    return b
}

func Min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
EOF

    cat > tests/auth_test.go << 'EOF'
package tests

import "testing"

func TestLogin(t *testing.T) {
    // TODO: implement
}
EOF

    # Commit initial state
    git add -A
    git commit -q -m "Initial commit"

    success "Test project created with $(find . -name '*.go' | wc -l | tr -d ' ') Go files"
}

# Create output directory
setup_output() {
    mkdir -p "$OUTPUT_DIR"
    log "Output will be saved to: $OUTPUT_DIR"
}

# Start watcher in background
start_watcher() {
    log "Starting watcher..."
    cd "$TEST_PROJECT"
    "$CODEMAP_BIN" --watch --debug . > "$OUTPUT_DIR/watcher.log" 2>&1 &
    WATCHER_PID=$!
    echo $WATCHER_PID > "$OUTPUT_DIR/watcher.pid"
    sleep 2  # Let it initialize
    success "Watcher started (PID: $WATCHER_PID)"
}

# Stop watcher
stop_watcher() {
    if [ -f "$OUTPUT_DIR/watcher.pid" ]; then
        WATCHER_PID=$(cat "$OUTPUT_DIR/watcher.pid")
        kill $WATCHER_PID 2>/dev/null || true
        wait $WATCHER_PID 2>/dev/null || true
        log "Watcher stopped"
    fi
}

# Scenario: Simple edit
scenario_simple_edit() {
    log "Scenario: Simple edit to existing file"
    cd "$TEST_PROJECT"

    # Add a function
    cat >> src/auth/auth.go << 'EOF'

func ValidateToken(token string) bool {
    return len(token) > 10
}
EOF
    sleep 0.5
}

# Scenario: Create new file
scenario_new_file() {
    log "Scenario: Create new file"
    cd "$TEST_PROJECT"

    cat > src/auth/tokens.go << 'EOF'
package auth

import (
    "crypto/rand"
    "encoding/hex"
)

func GenerateToken() string {
    bytes := make([]byte, 32)
    rand.Read(bytes)
    return hex.EncodeToString(bytes)
}

func RefreshToken(old string) string {
    return GenerateToken()
}
EOF
    sleep 0.5
}

# Scenario: Multiple rapid edits
scenario_rapid_edits() {
    log "Scenario: Multiple rapid edits (simulating active coding)"
    cd "$TEST_PROJECT"

    for i in 1 2 3 4 5; do
        echo "// Edit $i - $(date +%H:%M:%S)" >> src/api/server.go
        sleep 0.3
    done
    sleep 0.5
}

# Scenario: Refactor - move function
scenario_refactor() {
    log "Scenario: Refactor - add to utils, remove from auth"
    cd "$TEST_PROJECT"

    # Add new utility
    cat >> src/utils/helpers.go << 'EOF'

func ValidateEmail(email string) bool {
    return len(email) > 5 && strings.Contains(email, "@")
}
EOF
    sleep 0.3

    # Add import (edit existing)
    sed -i '' 's/package utils/package utils\n\nimport "strings"/' src/utils/helpers.go
    sleep 0.5
}

# Scenario: Delete file
scenario_delete_file() {
    log "Scenario: Delete a file"
    cd "$TEST_PROJECT"

    rm src/utils/helpers.go
    sleep 0.5
}

# Scenario: Create and immediately edit
scenario_create_edit() {
    log "Scenario: Create file then immediately edit"
    cd "$TEST_PROJECT"

    cat > src/api/middleware.go << 'EOF'
package api

func LogRequest() {
    // TODO
}
EOF
    sleep 0.2

    cat >> src/api/middleware.go << 'EOF'

func AuthMiddleware() {
    // Check auth token
}

func RateLimiter() {
    // Limit requests
}
EOF
    sleep 0.5
}

# Scenario: Test file changes
scenario_test_changes() {
    log "Scenario: Writing tests"
    cd "$TEST_PROJECT"

    cat > tests/api_test.go << 'EOF'
package tests

import "testing"

func TestHandleRequest(t *testing.T) {
    result := handleRequest("/health")
    if result != "OK: /health" {
        t.Errorf("Expected OK, got %s", result)
    }
}

func TestMiddleware(t *testing.T) {
    // TODO
}
EOF
    sleep 0.3

    cat >> tests/auth_test.go << 'EOF'

func TestValidateToken(t *testing.T) {
    if !ValidateToken("verylongtoken123") {
        t.Error("Should be valid")
    }
}
EOF
    sleep 0.5
}

# Generate summary report
generate_report() {
    log "Generating report..."

    # Copy events log
    cp "$TEST_PROJECT/.codemap/events.log" "$OUTPUT_DIR/" 2>/dev/null || true

    # Generate summary
    cat > "$OUTPUT_DIR/summary.md" << EOF
# Watch Test Report
Generated: $(date)

## Test Project
- Location: $TEST_PROJECT
- Files: $(find "$TEST_PROJECT" -name '*.go' 2>/dev/null | wc -l | tr -d ' ') Go files

## Scenarios Run
1. Simple edit - add function to auth.go
2. New file - create tokens.go
3. Rapid edits - 5 quick edits to server.go
4. Refactor - modify utils
5. Delete file - remove helpers.go
6. Create + edit - new middleware.go with additions
7. Test changes - add/modify test files

## Events Captured
\`\`\`
$(cat "$OUTPUT_DIR/events.log" 2>/dev/null || echo "No events captured")
\`\`\`

## Watcher Output
\`\`\`
$(cat "$OUTPUT_DIR/watcher.log" 2>/dev/null | head -50 || echo "No output")
\`\`\`

## Analysis
- Total events: $(wc -l < "$OUTPUT_DIR/events.log" 2>/dev/null || echo 0)
- Files created: $(grep -c "CREATE" "$OUTPUT_DIR/events.log" 2>/dev/null || echo 0)
- Files modified: $(grep -c "WRITE" "$OUTPUT_DIR/events.log" 2>/dev/null || echo 0)
- Files deleted: $(grep -c "REMOVE" "$OUTPUT_DIR/events.log" 2>/dev/null || echo 0)
- Dirty files: $(grep -c "dirty" "$OUTPUT_DIR/events.log" 2>/dev/null || echo 0)
EOF

    success "Report saved to: $OUTPUT_DIR/summary.md"
}

# Main test runner
main() {
    echo ""
    echo "======================================"
    echo "  Codemap Watch Test Harness"
    echo "======================================"
    echo ""

    # Check binary exists
    if [ ! -f "$CODEMAP_BIN" ]; then
        echo "Error: codemap-test binary not found at $CODEMAP_BIN"
        echo "Run: go build -o codemap-test ."
        exit 1
    fi

    setup_output
    setup_test_project
    start_watcher

    echo ""
    log "Running scenarios..."
    echo ""

    scenario_simple_edit
    scenario_new_file
    scenario_rapid_edits
    scenario_refactor
    scenario_delete_file
    scenario_create_edit
    scenario_test_changes

    echo ""
    log "Waiting for events to flush..."
    sleep 2

    stop_watcher
    generate_report

    echo ""
    echo "======================================"
    success "Test complete!"
    echo ""
    echo "Review outputs at:"
    echo "  $OUTPUT_DIR/"
    echo ""
    echo "Files:"
    ls -la "$OUTPUT_DIR/"
    echo ""
}

# Cleanup handler
cleanup() {
    stop_watcher
}
trap cleanup EXIT

# Run
main "$@"
