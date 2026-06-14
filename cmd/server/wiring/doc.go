// Package wiring contains constructor functions ("wirers") that
// instantiate seasonfill's bounded contexts.
//
// Each constructor returns a Bundle struct holding the wired
// collaborators for one bounded context. Bundles are passed by
// reference to higher layers (cmd/server.server.go, other wiring
// constructors).
//
// Import rules (enforced by convention; future arch_test may codify):
//   - wiring/<area>.go may import application/, infrastructure/,
//     interface/, internal/, domain/, and cmd/server/adapters.
//   - wiring/<area>.go MUST NOT import cmd/server, cmd/server/loops,
//     or other wiring/<area>.go directly. Cross-area dependencies
//     flow via Bundle references passed into the constructor.
package wiring
