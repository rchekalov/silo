// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"os"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/spf13/cobra"
)

var lspCmd = &cobra.Command{
	Use:   "lsp <tool>",
	Short: "Run the LSP server for <tool> in a container and proxy stdio",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tool := args[0]
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		def, ok := cfg.Tools[tool]
		if !ok {
			return errs.ToolNotInstalledError(tool)
		}
		if def.LSP == nil {
			return errs.Configf("tool %q has no LSP configuration", tool)
		}
		ws, err := config.ResolveWorkspace("")
		if err != nil {
			return err
		}
		projectRoot := ws.ProjectRoot
		merged := ws.Merged
		projectDir, err := ws.ProjectDir()
		if err != nil {
			return err
		}
		e := engine.NewContainerEngine(cfg)
		if err := e.EnsureRuntime(); err != nil {
			return err
		}
		exit, err := e.RunLSP(engine.RunLSPOptions{
			ToolName:      tool,
			Tool:          def,
			ProjectDir:    projectDir,
			ProjectRoot:   projectRoot,
			ProjectConfig: merged,
		})
		if err != nil {
			return err
		}
		if exit != 0 {
			os.Exit(int(exit))
		}
		return nil
	},
}

func init() { addCommand(lspCmd) }
