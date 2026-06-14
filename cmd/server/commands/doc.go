// Package commands implements seasonfill's CLI subcommands.
//
// Subcommands are dispatched from cmd/server/main.go. Each top-level
// function exported by this package is one subcommand entry point and
// is invoked with the residual os.Args slice (post-subcommand-name).
package commands
