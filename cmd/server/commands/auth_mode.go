package commands

import (
	"fmt"
	"os"
)

// AuthMode implements `seasonfill auth-mode`. Disabled during
// D-2..D-5 — depends on runtime_config repositories that are
// stubbed pending the auth schema rewrite.
func AuthMode(args []string) error {
	_ = args
	fmt.Fprintln(os.Stderr, "command disabled — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
	return nil
}
