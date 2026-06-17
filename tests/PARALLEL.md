# Parallel Tests Policy

Все тесты в seasonfill должны помечаться `t.Parallel()` первой строкой `Test*` функции, кроме случаев из whitelist ниже.

Subtests (`t.Run`) — внутри parallel-помеченного parent также вызывают `t.Parallel()`, чтобы не сериализоваться на `t.Run` boundary.

## Whitelist — тесты БЕЗ `t.Parallel()`

| File | Reason |
|------|--------|
| `interface/http/handlers/timezone_test.go` | `t.Setenv("TZ", ...)` в helper |
| `internal/config/config_test.go` | `t.Setenv("SEASONFILL_*", ...)` per test |
| `internal/runtime/tz/resolver_test.go` | `t.Setenv("TZ", ...)` per test |
| `cmd/server/main_test.go` | `t.Setenv("SEASONFILL_DATABASE_*", ...)` + bootstrap |
| `cmd/server/main_reload_e2e_test.go` | `t.Setenv` heavy + lifecycle |
| `cmd/server/commands/auth_mode_test.go` | `t.Setenv("SEASONFILL_DATABASE_*", ...)` per test |
| `infrastructure/tmdb/client_test.go` | Reads process-global Prometheus registry via `countersFromMetrics`/`observability.WritePrometheus`; multiple tests assert delta on shared counters (e.g. `tmdb_rate_limit_pauses_total`) and false-positive cross-contaminate under `t.Parallel()` |
| `application/bootstrap/apikey_test.go::TestResolveAPIKey_AutoGenKeyNotInSlog` + `::TestResolveAPIKey_NoKeyNoRowAutoGen` | Both touch `os.Stdout` (one swaps it via `captureStdout`, the other writes the auto-gen marker line); rest of file is parallel |

## Known flaky (УЖЕ parallel — оставляем, мониторим)

| File | Tests | Note |
|------|-------|------|
| `infrastructure/reload/scheduler_subscriber_test.go` | `TestSchedulerSubscriber_HotSwap_*`, `TestSchedulerSubscriber_RebuildOnChange` | Race-timing flake под `-race`; re-run policy |
| `infrastructure/reload/sonarr_clients_subscriber_test.go` | `TestDrain_SweeperFiresAndCleansUp` | Race-timing flake; уже parallel |

## Why this matters

1. **CI speedup**: `t.Parallel()` позволяет `go test -p N` запускать тесты в N горутин в рамках одного package.
2. **Force-multiplier for thread-safe fakes**: parallel exposes shared-state bugs в test helpers/mocks раньше.
3. **`-race -count=2`**: парный запуск под race detector ловит test-local data races.

## How to add a parallel test

```go
func TestSomething(t *testing.T) {
    t.Parallel()
    // ... rest of the test
}

// With subtests:
func TestSomethingTable(t *testing.T) {
    t.Parallel()
    cases := []struct{ ... }{...}
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()
            // ...
        })
    }
}
```

## Verify coverage

```bash
total=0; with_p=0
for f in $(find interface -name "*_test.go"); do
  has_test=$(grep -E "^func Test[A-Z][A-Za-z0-9_]*\b" "$f" | grep -vc "func TestMain")
  if [ "$has_test" -gt 0 ]; then
    total=$((total+1))
    grep -q "t.Parallel()" "$f" && with_p=$((with_p+1))
  fi
done
echo "interface/ parallel: $with_p / $total = $(( 100 * with_p / total ))%"

go test -race -count=2 -timeout 5m ./interface/...
```

If a new test cannot be parallel (uses `t.Setenv`, `signal.Notify`, shared globals), add it to the whitelist with one-line reason.
