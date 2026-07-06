package commands

import (
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/runner"
)

// resolveEngine picks the container engine for a recovery/lifecycle command
// (reset, forget, rehome). One policy for all three: honor the configured
// engine, but tolerate a broken byre.config — a config problem must never
// block wiping or migrating state — by falling back to auto-detect with a
// warning on stderr. Commands that need a valid config anyway (develop,
// rebuild) detect fatally from it instead; informational commands (status,
// dockerrun) keep their own best-effort semantics.
func resolveEngine(stderr io.Writer, projectDir string) (*runner.Runner, error) {
	engine := "auto"
	if cfg, err := config.Load(projectDir); err == nil {
		engine = cfg.Engine
	} else {
		fmt.Fprintf(stderr, "byre: warning: config did not load (%v); using engine=auto\n", err)
	}
	eng, err := runner.Detect(engine, nil)
	if err != nil {
		return nil, err
	}
	return runner.New(eng), nil
}
