package wiring

// watchdog.go owns the wiring for the watchdog bounded context:
// the healthcheck + state watchdog (Story 052), the Phase 10 regrab
// stack (Story 337 — settings UC, regrab UC, regrab loop, watchdog
// HTTP handlers), and the qBit settings reload-fanout loader.
