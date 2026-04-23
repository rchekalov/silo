// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
)

var currentCmd = &cobra.Command{
	Use:   "current [tool]",
	Short: "Show the effective tool definition after project overrides",
	Long: `Print the merged (global + .siloconf override) tool definition for <tool>,
or a summary of all installed tools with any active overrides when no tool
name is given.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCurrent,
}

func init() { addCommand(currentCmd) }

func runCurrent(cmd *cobra.Command, args []string) error {
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	ws, err := config.ResolveWorkspace("")
	if err != nil {
		return err
	}
	merged := ws.Merged

	if len(args) == 1 {
		name := args[0]
		def, ok := global.Tools[name]
		if !ok {
			return errs.ToolNotInstalledError(name)
		}
		if merged != nil {
			if o, ok := merged.Overrides[name]; ok {
				def = config.ApplyOverride(def, o)
			}
		}
		out, err := yaml.Marshal(def)
		if err != nil {
			return err
		}
		fmt.Printf("# %s\n%s", name, string(out))
		return nil
	}

	if len(global.Tools) == 0 {
		fmt.Println("No tools installed.")
		return nil
	}
	names := make([]string, 0, len(global.Tools))
	for n := range global.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Printf("Installed tools (%d):\n", len(global.Tools))
	for _, name := range names {
		t := global.Tools[name]
		overrideImage := ""
		if merged != nil {
			if o, ok := merged.Overrides[name]; ok && o.Image != "" {
				overrideImage = o.Image
			}
		}
		if overrideImage != "" {
			fmt.Printf("  %-20s %s (project: %s)\n", name, t.Image, overrideImage)
		} else {
			fmt.Printf("  %-20s %s\n", name, t.Image)
		}
	}
	return nil
}
