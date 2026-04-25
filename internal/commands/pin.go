// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
)

var pinCmd = &cobra.Command{
	Use:   "pin <tool>",
	Short: "Claim <tool> globally so silo handles its shim everywhere",
	Long: `Set pinnedGlobally:true for <tool> in ~/.silo/config.yaml.

After pinning, the tool's shim (e.g. "python") always dispatches into silo,
regardless of whether the current directory is inside a silo project. This
matches the behavior of running ` + "`silo install <tool>`" + ` initially.

Use this to flip a sync-installed tool back to global ownership without
re-running the install pipeline.`,
	Args: cobra.ExactArgs(1),
	RunE: runPin,
}

var unpinCmd = &cobra.Command{
	Use:   "unpin <tool>",
	Short: "Drop the global pin from <tool> so it falls through outside projects",
	Long: `Set pinnedGlobally:false for <tool> in ~/.silo/config.yaml.

After unpinning, invoking the tool's shim outside any project that claims it
in .siloconf transparently execs the next instance on PATH (e.g. homebrew's
binary). Inside a project that lists the tool, silo still handles it.

Use this when you ran ` + "`silo install`" + ` for a tool but want to revert
to project-scoped ownership (the default for ` + "`silo sync`" + `-installed
tools).`,
	Args: cobra.ExactArgs(1),
	RunE: runUnpin,
}

func init() {
	addCommand(pinCmd)
	addCommand(unpinCmd)
}

func runPin(_ *cobra.Command, args []string) error {
	return setPin(args[0], true)
}

func runUnpin(_ *cobra.Command, args []string) error {
	return setPin(args[0], false)
}

func setPin(name string, pinned bool) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	def, ok := cfg.Tools[name]
	if !ok {
		return errs.ToolNotInstalledError(name)
	}
	if def.PinnedGlobally == pinned {
		state := "already pinned globally"
		if !pinned {
			state = "already not globally pinned"
		}
		fmt.Printf("%s is %s.\n", name, state)
		return nil
	}
	def.PinnedGlobally = pinned
	if err := cfg.InstallTool(name, def); err != nil {
		return err
	}
	if pinned {
		fmt.Printf("Pinned %s globally. Its shim will always dispatch into silo.\n", name)
	} else {
		fmt.Printf("Unpinned %s. Its shim falls through to the next instance on PATH outside silo projects.\n", name)
	}
	return nil
}
