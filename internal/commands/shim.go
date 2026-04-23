// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/shim"
)

var shimCmd = &cobra.Command{
	Use:   "shim",
	Short: "Manage per-tool shim scripts",
}

var shimAddCmd = &cobra.Command{
	Use:   "add <tool> <shim> [<shim>...]",
	Short: "Add one or more shims to a tool",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		tool := args[0]
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		def, ok := cfg.Tools[tool]
		if !ok {
			return errs.ToolNotInstalledError(tool)
		}
		sm := shim.NewManager("")
		for _, spec := range args[1:] {
			s := config.ParseShim(spec)
			// Avoid duplicates
			dup := false
			for _, existing := range def.Shims {
				if existing.HostCommand == s.HostCommand {
					dup = true
					break
				}
			}
			if dup {
				fmt.Fprintf(os.Stderr, "Shim %q already exists on %q\n", s.HostCommand, tool)
				continue
			}
			def.Shims = append(def.Shims, s)
			if err := sm.CreateShim(s, tool); err != nil {
				return err
			}
		}
		return cfg.InstallTool(tool, def)
	},
}

var shimRemoveCmd = &cobra.Command{
	Use:   "remove <tool> <shim> [<shim>...]",
	Short: "Remove one or more shims from a tool",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		tool := args[0]
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		def, ok := cfg.Tools[tool]
		if !ok {
			return errs.ToolNotInstalledError(tool)
		}
		sm := shim.NewManager("")
		remove := map[string]bool{}
		for _, s := range args[1:] {
			remove[s] = true
		}
		kept := def.Shims[:0]
		for _, s := range def.Shims {
			if remove[s.HostCommand] {
				_ = sm.RemoveShim(s.HostCommand)
				continue
			}
			kept = append(kept, s)
		}
		def.Shims = kept
		return cfg.InstallTool(tool, def)
	},
}

var shimListCmd = &cobra.Command{
	Use:   "list <tool>",
	Short: "Show shims for a tool",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		def, ok := cfg.Tools[args[0]]
		if !ok {
			return errs.ToolNotInstalledError(args[0])
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "HOST COMMAND\tCONTAINER COMMAND")
		for _, s := range def.Shims {
			fmt.Fprintf(w, "%s\t%s\n", s.HostCommand, s.ContainerCommand)
		}
		return w.Flush()
	},
}

func init() {
	shimCmd.AddCommand(shimAddCmd, shimRemoveCmd, shimListCmd)
	addCommand(shimCmd)
}
