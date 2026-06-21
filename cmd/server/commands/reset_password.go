package commands

import (
	"fmt"
	"os"
)

// ResetPassword implements `seasonfill reset-password`. Disabled during
// D-2..D-5 — depends on admin user repository that is stubbed pending
// the auth schema rewrite.
func ResetPassword(args []string) error {
	_ = args
	fmt.Fprintln(os.Stderr, "command disabled — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
	return nil
}
