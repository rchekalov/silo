// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"os"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/spf13/cobra"
)

var shellCmd = &cobra.Command{
	Use:   "shell <tool>",
	Short: "Interactive shell in an ephemeral container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		tool, def, _, err := resolveToolOrShim(cfg, args[0])
		if err != nil {
			return err
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
		exit, err := e.RunEphemeral(engine.RunEphemeralOptions{
			ToolName:      tool,
			Tool:          def,
			Command:       "/bin/sh",
			ProjectDir:    projectDir,
			ProjectRoot:   projectRoot,
			ProjectConfig: merged,
			Interactive:   true,
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

func init() { addCommand(shellCmd) }
