// Package loops contains seasonfill's per-instance and global
// background polling loops.
//
// Each loop type exposes a Run(context.Context) method consumed by
// cmd/server/server.go via lifecycleGroup.Go (for inline goroutines
// owned by the Server struct) or via the *sync.WaitGroup pattern
// (for loops whose spawning is fanned out from reload subscribers).
//
// Loops MUST NOT import cmd/server, cmd/server/wiring, or
// cmd/server/adapters — they consume only application/,
// infrastructure/, interface/, and internal/ symbols.
package loops
