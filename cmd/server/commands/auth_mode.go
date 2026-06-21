package commands

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// AuthMode implements `seasonfill auth-mode --get`. The --set path is
// re-enabled in 466b once the RuntimeConfigRepository rewrite lands. In
// 466a the --get path returns the runtime default mode (`forms`) —
// sufficient for operators to confirm the binary works without booting
// the full pod.
func AuthMode(args []string) error {
	fs := flag.NewFlagSet("auth-mode", flag.ContinueOnError)
	getFlag := fs.Bool("get", false, "print current auth mode")
	setFlag := fs.String("set", "", "set auth mode (forms|basic|none|oidc) — pending 466b")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if !*getFlag && *setFlag == "" {
		return errors.New("--get or --set <mode> required")
	}
	if *getFlag && *setFlag != "" {
		return errors.New("--get and --set are mutually exclusive")
	}
	if *setFlag != "" {
		return errors.New("--set is disabled until 466b runtime_config rewrite")
	}
	if _, err := fmt.Fprintf(os.Stdout, "%s\n", runtime.Defaults().Auth.Mode); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
