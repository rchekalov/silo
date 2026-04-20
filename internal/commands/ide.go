// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"os"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/lsp"
	"github.com/spf13/cobra"
)

var ideToolFilter string

var ideCmd = &cobra.Command{
	Use:   "ide <vscode|zed|neovim>",
	Short: "Generate IDE configuration for Silo LSP servers",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ide := args[0]
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		var tools map[string]config.ToolDefinition
		if ideToolFilter != "" {
			def, ok := cfg.Tools[ideToolFilter]
			if !ok {
				return errs.ToolNotInstalledError(ideToolFilter)
			}
			tools = map[string]config.ToolDefinition{ideToolFilter: def}
		} else {
			tools = cfg.Tools
		}
		return lsp.GenerateIDEConfig(ide, tools, cwd)
	},
}

func init() {
	ideCmd.Flags().StringVar(&ideToolFilter, "tool", "", "only generate config for this tool")
	addCommand(ideCmd)
}
