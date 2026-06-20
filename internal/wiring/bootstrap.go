package wiring

// bootstrap.go owns the wiring for the boot/runtime bedrock that
// every other bounded context depends on:
//
//   - PersistenceBundle / BuildPersistence — DB + bedrock repos + crypto
//     + tz resolver (the cross-cutting handles every wirer reads).
//   - HTTPServeConfig / RuntimeConfigBundle / BuildRuntimeConfig —
//     runtime-config snapshot + UC + handler + HTTPServeConfig DTO.
//   - SchedulerBundle / BuildScheduler — cron factory + boot scheduler
//     with every cron job registered.
//   - BuildOnAppliedFanout / SubscriberDeps / StartSubscribers —
//     reload-bus subscribers + the fan-out closure.
//   - BuildHTTPServer — the 37-arg root composer.
//
// The "kernel" relationship: bootstrap.go imports from every
// per-context file in this package; per-context files only import
// internal/, application/, infrastructure/. The depcheck guard
// (tests/lint_wiring_imports_test.go) enforces this asymmetry.
