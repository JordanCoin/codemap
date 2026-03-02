# Codex Task: Improve Test Coverage

## Objective

Incrementally improve test coverage toward 90%. Each run should target **one package**, add tests, and open a PR.

## Steps

1. **Measure current coverage per package:**

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | awk '/^total:/ {print "TOTAL:", $3}'
# Per-package breakdown:
go test -cover ./... 2>&1 | sort -t'%' -k2 -n
```

2. **Pick the lowest-coverage package** from this priority list (skip packages already above 80%):

| Priority | Package | Notes |
|----------|---------|-------|
| 1 | `scanner/` | Core scanning logic, high impact |
| 2 | `watch/` | Daemon/event system |
| 3 | `render/` | Output rendering |
| 4 | `handoff/` | Handoff artifact building |
| 5 | `cmd/` | CLI commands |
| 6 | `config/` | Configuration loading |
| 7 | `limits/` | Budget enforcement |

**Skip these packages** (hard to test in isolation):
- `mcp/` — MCP server, requires full stdio lifecycle
- Root `main.go` — thin entry point

3. **Write tests** following the project's existing patterns (see below).

4. **Check if the CI coverage floor can be bumped.** If total coverage is now ≥ current floor + 5 points, update the `min=` value in `.github/workflows/ci.yml` (the `Enforce coverage floor` step).

5. **Open a PR** with:
   - Title: `test: improve <pkg> coverage from X% to Y%`
   - Body: before/after per-package and total coverage numbers
   - Max **500 lines** of new test code per PR

## Test Patterns to Follow

All existing tests in this repo use these conventions. Follow them exactly.

### Table-driven tests

```go
func TestFoo(t *testing.T) {
    tests := []struct {
        name string
        // inputs
        // expected outputs
    }{
        {name: "descriptive case name", /* ... */},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test body
        })
    }
}
```

### Standard library only

- Use `testing` package only — no testify, no gomock, no external test deps.
- Use `t.Errorf` / `t.Fatalf` for assertions.
- Use `t.Helper()` in test helper functions.

### Temp directories

```go
dir := t.TempDir() // auto-cleaned up
```

### File setup in tests

```go
err := os.WriteFile(filepath.Join(dir, "file.go"), []byte(content), 0o644)
if err != nil {
    t.Fatal(err)
}
```

### Subtests for variants

Use `t.Run(tt.name, ...)` — never run test cases in a bare loop.

## Functions to Skip

These are difficult to unit test and should be skipped:

- Anything requiring a running subprocess (`exec.Command` wrappers)
- Terminal UI rendering (lipgloss/bubbletea output)
- `main()` and top-level CLI entry points
- Functions that require network access or a running MCP server
- Functions that depend on git operations against real repos (unless using `t.TempDir()` with `git init`)

## Rules

- **Never modify source code** — only add `_test.go` files.
- Add tests to the **existing** `_test.go` file for the package if one exists; create a new one only if needed.
- Keep tests deterministic — no sleep, no real network, no time-dependent assertions.
- Run `go test -race ./...` before opening the PR.
- Run `go vet ./...` and `gofmt -l .` — the PR must pass CI.
- Each test function should test **one behavior** clearly described by its name.

## PR Checklist

- [ ] `go test -race -coverprofile=coverage.out ./...` passes
- [ ] `go vet ./...` clean
- [ ] `gofmt -l .` shows no files
- [ ] No source files modified, only `_test.go` files added/changed
- [ ] Coverage floor bumped in CI if warranted (≥ floor + 5)
- [ ] PR title follows format: `test: improve <pkg> coverage from X% to Y%`
- [ ] PR body includes before/after coverage table
