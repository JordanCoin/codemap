#!/bin/bash
# Test MCP integration with watch mode

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MCP_BIN="${SCRIPT_DIR}/../../mcp-test"
TEST_PROJECT="/tmp/codemap-mcp-test"
OUTPUT_DIR="${SCRIPT_DIR}/output/mcp-$(date +%Y%m%d-%H%M%S)"

mkdir -p "$OUTPUT_DIR"

echo "=== MCP Watch Integration Test ==="
echo ""

# Create a simple test project
echo "[1] Creating test project..."
rm -rf "$TEST_PROJECT"
mkdir -p "$TEST_PROJECT/src"
cd "$TEST_PROJECT"
git init -q
git config user.email "test@test.com"
git config user.name "Test"

cat > src/main.go << 'EOF'
package main

import "fmt"

func main() {
    fmt.Println("Hello")
}
EOF

cat > src/utils.go << 'EOF'
package main

func helper() string {
    return "help"
}
EOF

git add -A && git commit -q -m "Initial"
echo "   Created project at $TEST_PROJECT"

# Function to call MCP tool
call_mcp() {
    local tool="$1"
    local params="$2"
    echo "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$tool\",\"arguments\":$params}}"
}

# Start MCP server in background, connected via named pipes
FIFO_IN="$OUTPUT_DIR/mcp_in"
FIFO_OUT="$OUTPUT_DIR/mcp_out"
mkfifo "$FIFO_IN" "$FIFO_OUT"

echo "[2] Starting MCP server..."
"$MCP_BIN" < "$FIFO_IN" > "$FIFO_OUT" 2>"$OUTPUT_DIR/mcp_stderr.log" &
MCP_PID=$!
sleep 1

# Open file descriptors
exec 3>"$FIFO_IN"
exec 4<"$FIFO_OUT"

# Initialize
echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' >&3

# Read init response (with timeout)
read -t 5 init_response <&4 || true
echo "   MCP server initialized"

# Send initialized notification
echo '{"jsonrpc":"2.0","method":"notifications/initialized"}' >&3

echo ""
echo "[3] Testing start_watch..."
call_mcp "start_watch" "{\"path\":\"$TEST_PROJECT\"}" >&3
sleep 1
read -t 5 response <&4 || true
echo "$response" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r.get('result',{}).get('content',[{}])[0].get('text','no response'))" 2>/dev/null || echo "$response"

echo ""
echo "[4] Making some edits..."
echo "// edit 1" >> "$TEST_PROJECT/src/main.go"
sleep 0.3
echo "// edit 2" >> "$TEST_PROJECT/src/main.go"
sleep 0.3
echo "// edit 3" >> "$TEST_PROJECT/src/utils.go"
sleep 0.3
cat > "$TEST_PROJECT/src/new_file.go" << 'EOF'
package main

func newFunc() {
    // brand new
}
EOF
sleep 1

echo ""
echo "[5] Testing get_activity..."
call_mcp "get_activity" "{\"path\":\"$TEST_PROJECT\",\"minutes\":5}" >&3
sleep 1
read -t 5 response <&4 || true
echo "$response" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r.get('result',{}).get('content',[{}])[0].get('text','no response'))" 2>/dev/null || echo "$response"

echo ""
echo "[6] Testing status..."
call_mcp "status" "{}" >&3
sleep 1
read -t 5 response <&4 || true
echo "$response" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r.get('result',{}).get('content',[{}])[0].get('text','no response'))" 2>/dev/null || echo "$response"

echo ""
echo "[7] Testing stop_watch..."
call_mcp "stop_watch" "{\"path\":\"$TEST_PROJECT\"}" >&3
sleep 1
read -t 5 response <&4 || true
echo "$response" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r.get('result',{}).get('content',[{}])[0].get('text','no response'))" 2>/dev/null || echo "$response"

# Cleanup
exec 3>&-
exec 4<&-
kill $MCP_PID 2>/dev/null || true
rm -f "$FIFO_IN" "$FIFO_OUT"

echo ""
echo "=== Test Complete ==="
echo "Output saved to: $OUTPUT_DIR"
