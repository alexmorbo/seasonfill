package commands

import (
	"context"
	"fmt"
	"os"
)

// Reparse is the CLI entry point for `seasonfill grabs reparse`.
// Disabled during D-2..D-5 — depends on admin user / runtime_config
// repositories that are stubbed pending the auth schema rewrite.
func Reparse(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	fmt.Fprintln(os.Stderr, "command disabled — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
	return nil
}
