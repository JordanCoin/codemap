# Watch Test Report
Generated: Sat Dec  6 14:46:23 EST 2025

## Test Project
- Location: /tmp/codemap-watch-test
- Files: 7 Go files

## Scenarios Run
1. Simple edit - add function to auth.go
2. New file - create tokens.go
3. Rapid edits - 5 quick edits to server.go
4. Refactor - modify utils
5. Delete file - remove helpers.go
6. Create + edit - new middleware.go with additions
7. Test changes - add/modify test files

## Events Captured
```
2025-12-06 14:46:15 | WRITE  | src/auth/auth.go                         |   19 |     +4 | dirty
2025-12-06 14:46:16 | CREATE | src/auth/tokens.go                       |    0 |        | 
2025-12-06 14:46:16 | WRITE  | src/api/server.go                        |   12 |     +1 | dirty
2025-12-06 14:46:17 | WRITE  | src/api/server.go                        |   13 |     +1 | dirty
2025-12-06 14:46:17 | WRITE  | src/api/server.go                        |   14 |     +1 | dirty
2025-12-06 14:46:17 | WRITE  | src/api/server.go                        |   15 |     +1 | dirty
2025-12-06 14:46:18 | WRITE  | src/api/server.go                        |   16 |     +1 | dirty
2025-12-06 14:46:18 | WRITE  | src/utils/helpers.go                     |   19 |     +4 | dirty
2025-12-06 14:46:19 | CREATE | src/utils/.!94219!helpers.go             |   21 |    +21 | 
2025-12-06 14:46:19 | REMOVE | src/utils/helpers.go                     |    0 |    -19 | 
2025-12-06 14:46:19 | REMOVE | src/utils/helpers.go                     |    0 |        | 
2025-12-06 14:46:20 | CREATE | src/api/middleware.go                    |    0 |        | 
2025-12-06 14:46:20 | WRITE  | src/api/middleware.go                    |   13 |    +13 | 
2025-12-06 14:46:20 | CREATE | tests/api_test.go                        |    0 |        | 
2025-12-06 14:46:21 | WRITE  | tests/auth_test.go                       |   13 |     +6 | dirty
```

## Watcher Output
```
codemap watch - Live code graph daemon

[watch] Full scan: 5 files in 316.875µs
Watching: /tmp/codemap-watch-test
Files tracked: 5
Event log: .codemap/events.log

Press Ctrl+C to stop

[watch] 14:46:15 WRITE src/auth/auth.go (+4 lines) [dirty]
[watch] 14:46:16 CREATE src/auth/tokens.go
[watch] 14:46:16 WRITE src/api/server.go (+1 lines) [dirty]
[watch] 14:46:17 WRITE src/api/server.go (+1 lines) [dirty]
[watch] 14:46:17 WRITE src/api/server.go (+1 lines) [dirty]
[watch] 14:46:17 WRITE src/api/server.go (+1 lines) [dirty]
[watch] 14:46:18 WRITE src/api/server.go (+1 lines) [dirty]
[14:46:18] Recent events:
  14:46:16 WRITE src/api/server.go
  14:46:17 WRITE src/api/server.go
  14:46:17 WRITE src/api/server.go
  14:46:17 WRITE src/api/server.go
  14:46:18 WRITE src/api/server.go
[watch] 14:46:18 WRITE src/utils/helpers.go (+4 lines) [dirty]
[watch] 14:46:19 CREATE src/utils/.!94219!helpers.go (+21 lines)
[watch] 14:46:19 REMOVE src/utils/helpers.go (-19 lines)
[watch] 14:46:19 REMOVE src/utils/helpers.go
[watch] 14:46:20 CREATE src/api/middleware.go
[watch] 14:46:20 WRITE src/api/middleware.go (+13 lines)
[watch] 14:46:20 CREATE tests/api_test.go
[watch] 14:46:21 WRITE tests/auth_test.go (+6 lines) [dirty]
[14:46:23] Recent events:
  14:46:19 REMOVE src/utils/helpers.go
  14:46:20 CREATE src/api/middleware.go
  14:46:20 WRITE src/api/middleware.go
  14:46:20 CREATE tests/api_test.go
  14:46:21 WRITE tests/auth_test.go

Shutting down...

Session summary:
  Files tracked: 8
  Events logged: 15
```

## Analysis
- Total events:       15
- Files created: 4
- Files modified: 9
- Files deleted: 2
- Dirty files: 8
