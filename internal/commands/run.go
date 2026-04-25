// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
)

var runCmd = &cobra.Command{
	Use:   "run <tool> [args...]",
	Short: "Run a command in an ephemeral container",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

var (
	runShim   string
	runTiming bool
)

// Flag tables consumed by cmd/silo/main.go to split argv into silo flags +
// tool name + pass-through. Keep in sync with the Flags() registrations below.
var (
	RunValueFlags = []string{"shim"}
	RunBoolFlags  = []string{"timing"}
)

func init() {
	runCmd.Flags().StringVar(&runShim, "shim", "", "override shim command")
	runCmd.Flags().BoolVar(&runTiming, "timing", false, "print timing info")
	addCommand(runCmd)
}

// passthroughArgs decodes args forwarded via _SILO_PASSTHROUGH (see cmd/silo/main.go).
func passthroughArgs() []string {
	raw := os.Getenv("_SILO_PASSTHROUGH")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\x1F")
}

func runRun(_ *cobra.Command, args []string) error {
	passthrough := passthroughArgs()
	total := time.Now()

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	tool, def, resolvedShim, err := resolveToolOrShim(cfg, args[0])
	if err != nil {
		return err
	}

	command := tool
	switch {
	case runShim != "":
		command = runShim
	case resolvedShim != "":
		command = resolvedShim
	}

	if runShim == "" && resolvedShim == "" {
		command, passthrough = applySiblingShim(cfg, tool, command, passthrough, os.Stderr)
	}

	ws, err := config.ResolveWorkspace("")
	if err != nil {
		return err
	}
	projectRoot := ws.ProjectRoot
	mergedCfg := ws.Merged
	projectDir, err := ws.ProjectDir()
	if err != nil {
		return err
	}

	if runTiming {
		fmt.Fprintf(os.Stderr, "[silo] config loaded: %dms\n", time.Since(total).Milliseconds())
	}

	e := engine.NewContainerEngine(cfg)
	if err := e.EnsureRuntime(); err != nil {
		return err
	}
	if runTiming {
		fmt.Fprintf(os.Stderr, "[silo] runtime ready: %dms\n", time.Since(total).Milliseconds())
	}

	vmStart := time.Now()
	exit, err := e.RunEphemeral(engine.RunEphemeralOptions{
		ToolName:      tool,
		Tool:          def,
		Command:       command,
		Arguments:     passthrough,
		ProjectDir:    projectDir,
		ProjectRoot:   projectRoot,
		ProjectConfig: mergedCfg,
		Interactive:   true,
	})
	if err != nil {
		return err
	}
	if runTiming {
		fmt.Fprintf(os.Stderr, "[silo] ephemeral completed: %dms\n", time.Since(vmStart).Milliseconds())
		fmt.Fprintf(os.Stderr, "[silo] total: %dms\n", time.Since(total).Milliseconds())
	}
	if exit != 0 {
		os.Exit(int(exit))
	}
	return nil
}

// applySiblingShim implements Docker-style entrypoint override when the first
// passthrough arg is a known shim. If it's a shim of the same tool, promote it
// to command and shift it off passthrough. If it's a shim of a different tool
// only, write a one-line hint to stderrW and leave args unchanged.
func applySiblingShim(
	cfg *config.GlobalConfig,
	tool, command string,
	passthrough []string,
	stderrW io.Writer,
) (string, []string) {
	if len(passthrough) == 0 {
		return command, passthrough
	}
	arg0 := passthrough[0]
	if arg0 == "" || arg0 == command || arg0 == tool || strings.HasPrefix(arg0, "-") {
		return command, passthrough
	}
	matches := cfg.ResolveShimAll(arg0)
	if len(matches) == 0 {
		return command, passthrough
	}
	for _, m := range matches {
		if m == tool {
			return arg0, passthrough[1:]
		}
	}
	rest := ""
	if len(passthrough) > 1 {
		rest = " " + strings.Join(passthrough[1:], " ")
	}
	fmt.Fprintf(stderrW,
		"silo: hint: %q is a shim of %s, not %s. Did you mean: silo run %s%s\n",
		arg0, strings.Join(matches, ", "), tool, arg0, rest)
	return command, passthrough
}
