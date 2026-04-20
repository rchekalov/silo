// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <tool> [-- <args>...]",
	Short: "Run a command in an ephemeral container",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

var (
	runShim   string
	runTiming bool
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

func runRun(cmd *cobra.Command, args []string) error {
	tool := args[0]
	passthrough := passthroughArgs()
	total := time.Now()

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	def, ok := cfg.Tools[tool]
	if !ok {
		return errs.ToolNotInstalledError(tool)
	}

	command := tool
	if runShim != "" {
		command = runShim
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
