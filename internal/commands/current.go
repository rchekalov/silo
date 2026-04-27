// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"io"
	"os"
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

func runCurrent(_ *cobra.Command, args []string) error {
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
		def = overlayRegistryNetwork(name, def)
		if merged != nil {
			if o, ok := merged.Overrides[name]; ok {
				def = config.ApplyOverride(def, o)
			}
		}
		out, err := yaml.Marshal(def)
		if err != nil {
			return err
		}
		fmt.Printf("# %s — %s\n%s", name, dispatchStatus(name, def, merged, ws.ProjectRoot), string(out))
		return nil
	}

	return renderCurrentSummary(os.Stdout, global, merged)
}

// renderCurrentSummary writes the multi-tool `silo current` summary to w.
// Each line gets a marker — [project] / [pinned] / [fall-through] — that
// mirrors dispatchStatus's three branches.
func renderCurrentSummary(w io.Writer, global *config.GlobalConfig, merged *config.ProjectConfig) error {
	if len(global.Tools) == 0 {
		_, err := fmt.Fprintln(w, "No tools installed.")
		return err
	}
	names := make([]string, 0, len(global.Tools))
	for n := range global.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "Installed tools (%d):\n", len(global.Tools))
	for _, name := range names {
		t := global.Tools[name]
		overrideImage := ""
		if merged != nil {
			if o, ok := merged.Overrides[name]; ok && o.Image != "" {
				overrideImage = o.Image
			}
		}
		marker := currentMarker(name, t.PinnedGlobally, merged)
		if overrideImage != "" {
			fmt.Fprintf(w, "  %-20s %s (project: %s) %s\n", name, t.Image, overrideImage, marker)
		} else {
			fmt.Fprintf(w, "  %-20s %s %s\n", name, t.Image, marker)
		}
	}
	return nil
}

// currentMarker renders the bracketed dispatch tag shown in `silo current`'s
// multi-tool view. Precedence matches dispatchStatusFor: project claim wins
// over pin, and fall-through is the default.
func currentMarker(tool string, pinnedGlobally bool, merged *config.ProjectConfig) string {
	switch {
	case merged != nil && merged.Claims(tool):
		return "[project]"
	case pinnedGlobally:
		return "[pinned]"
	default:
		return "[fall-through]"
	}
}

// dispatchStatus describes how a shim invocation of `name` will be handled
// from the current cwd: routed into silo by a project claim, by a global pin,
// or transparently passed through to the next instance on PATH.
//
// Thin wrapper around dispatchStatusFor — the latter takes primitives so
// rendering tests don't have to construct full ToolDefinition / ProjectConfig
// values.
func dispatchStatus(name string, def config.ToolDefinition, merged *config.ProjectConfig, projectRoot string) string {
	claimed := merged != nil && merged.Claims(name)
	return dispatchStatusFor(claimed, def.PinnedGlobally, projectRoot)
}

// dispatchStatusFor is the rendering-only core of dispatchStatus. Three
// inputs:
//
//   - claimed: the project (merged with global siloconf) lists this tool
//   - pinnedGlobally: ~/.silo/config.yaml has pinnedGlobally:true
//   - projectRoot: where the .siloconf was found ("" => global siloconf only)
//
// Precedence matches the dispatch in run.go: project claim > global pin >
// fall-through. The empty `projectRoot` with `claimed=true` means the user
// declared the tool in ~/.silo/siloconf rather than a per-project file.
func dispatchStatusFor(claimed, pinnedGlobally bool, projectRoot string) string {
	if claimed {
		if projectRoot != "" {
			return fmt.Sprintf("claimed by %s", projectRoot)
		}
		return "claimed by global ~/.silo/siloconf"
	}
	if pinnedGlobally {
		return "globally pinned"
	}
	return "fall-through (no project claim, not pinned globally)"
}
