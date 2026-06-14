// Package adapters bridges between application/, infrastructure/, and
// interface/ layer interfaces.
//
// Adapters are pure type adapters with no state beyond what they wrap.
// They MUST NOT import cmd/server, cmd/server/wiring, or
// cmd/server/loops — they consume only application/, infrastructure/,
// interface/, domain/, and internal/.
package adapters
