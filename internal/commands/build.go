// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
)

var (
	buildGlobal bool
	buildRemove bool
	buildRerun  bool
	buildAll    bool
	buildScript string
)

var buildCmd = &cobra.Command{
	Use:                   "build <tool> [command...]",
	Aliases:               []string{"setup", "rebuild"},
	Short:                 "Build a persistent rootfs by running a setup command",
	DisableFlagsInUseLine: true,
	Long: `Build a persistent customized rootfs for <tool>. The tool's image plus
anything done by the setup command becomes the new starting point for later
'silo run <tool>' invocations in this project (or globally with --global).

  silo build node npm i -g typescript
  silo build node --rerun              # re-run the stored command
  silo build node --remove             # delete the stored rootfs
  silo build --all --rerun             # refresh every tool with a stored script

The legacy '--' separator (silo build node -- npm install) is still accepted.`,
	Args: cobra.ArbitraryArgs,
	RunE: runBuild,
}

// Flag tables consumed by cmd/silo/main.go to split argv into silo flags +
// tool name + pass-through. Keep in sync with the Flags() registrations below.
var (
	BuildValueFlags = []string{"script", "setup"}
	BuildBoolFlags  = []string{"global", "remove", "rerun", "all", "reset"}
)

func init() {
	buildCmd.Flags().BoolVar(&buildGlobal, "global", false, "persist in ~/.silo/builds/<tool> (instead of .silo/<tool> next to .siloconf)")
	buildCmd.Flags().BoolVar(&buildRemove, "remove", false, "delete the stored rootfs instead of building")
	buildCmd.Flags().BoolVar(&buildRerun, "rerun", false, "re-run the stored build script")
	buildCmd.Flags().BoolVar(&buildAll, "all", false, "rebuild every tool with a stored script (implies --rerun)")
	buildCmd.Flags().StringVar(&buildScript, "script", "", "override the stored script for --rerun")
	// Legacy flag names from the old `setup`/`rebuild` commands:
	buildCmd.Flags().BoolVar(&buildRemove, "reset", false, "alias for --remove (deprecated)")
	buildCmd.Flags().StringVar(&buildScript, "setup", "", "alias for --script (deprecated)")
	addCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	deprecation := ""
	switch cmd.CalledAs() {
	case "setup":
		deprecation = "`silo setup` is now `silo build`; setup will be removed in 0.6.0."
	case "rebuild":
		deprecation = "`silo rebuild` is now `silo build --rerun`; rebuild will be removed in 0.6.0."
		if !buildRerun && !buildAll {
			buildRerun = true
		}
	}
	if deprecation != "" {
		fmt.Fprintf(os.Stderr, "note: %s\n", deprecation)
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	if buildAll {
		return runBuildAll(cfg)
	}

	if len(args) < 1 {
		return errs.Configf("specify a tool name (or pass --all with --rerun)")
	}
	tool := args[0]
	passthrough := passthroughArgs()

	def, ok := cfg.Tools[tool]
	if !ok {
		return errs.ToolNotInstalledError(tool)
	}

	ws, err := config.ResolveWorkspace("")
	if err != nil {
		return err
	}

	target, isGlobal, err := resolveBuildTarget(tool, def, ws.ProjectRoot)
	if err != nil {
		return err
	}

	if buildRemove {
		return removeBuild(target)
	}

	command, arguments, err := resolveBuildCommand(tool, def, passthrough)
	if err != nil {
		return err
	}

	return runBuildOnce(cfg, ws, tool, def, target, isGlobal, command, arguments)
}

func runBuildAll(cfg *config.GlobalConfig) error {
	if !buildRerun {
		buildRerun = true // --all implies --rerun
	}
	ws, err := config.ResolveWorkspace("")
	if err != nil {
		return err
	}
	var targets []string
	for name, def := range cfg.Tools {
		if def.BuildScript != "" {
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		return errs.Configf("no tools have a stored build script; run `silo build <tool> <cmd>` first")
	}
	for _, tool := range targets {
		def := cfg.Tools[tool]
		target, isGlobal, err := resolveBuildTarget(tool, def, ws.ProjectRoot)
		if err != nil {
			return err
		}
		command, arguments, err := resolveBuildCommand(tool, def, nil)
		if err != nil {
			return err
		}
		if err := runBuildOnce(cfg, ws, tool, def, target, isGlobal, command, arguments); err != nil {
			return err
		}
	}
	return nil
}

func resolveBuildTarget(tool string, def config.ToolDefinition, projectRoot string) (string, bool, error) {
	if buildGlobal {
		return runtime.GlobalBuildRootfs(tool), true, nil
	}
	if buildRerun {
		// --rerun respects the scope recorded at build time.
		return resolveRebuildScope(tool, def, projectRoot, false)
	}
	if projectRoot == "" {
		return "", false, errs.Configf("no project root found (no .siloconf in parent directories). Pass --global for a system-wide build.")
	}
	return runtime.ProjectRootfs(projectRoot, tool), false, nil
}

func resolveBuildCommand(tool string, def config.ToolDefinition, passthrough []string) (string, []string, error) {
	if buildRerun {
		script := buildScript
		if script == "" {
			script = def.BuildScript
		}
		if script == "" {
			return "", nil, errs.Configf("%s: no build script stored (run `silo build %s -- ...` first)", tool, tool)
		}
		return "sh", []string{"-c", script}, nil
	}
	if len(passthrough) == 0 {
		return "", nil, fmt.Errorf("build requires a command, e.g. silo build %s npm i -g typescript", tool)
	}
	return passthrough[0], passthrough[1:], nil
}

func runBuildOnce(
	cfg *config.GlobalConfig,
	ws config.Workspace,
	tool string,
	def config.ToolDefinition,
	target string,
	isGlobal bool,
	command string,
	arguments []string,
) error {
	e := engine.NewContainerEngine(cfg)
	if err := e.EnsureRuntime(); err != nil {
		return err
	}
	projectDir, err := ws.ProjectDir()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Building %s via: %s %s\n", tool, command, strings.Join(arguments, " "))
	exit, err := e.RunSetup(engine.RunSetupOptions{
		ToolName:      tool,
		Tool:          def,
		Command:       command,
		Arguments:     arguments,
		ProjectDir:    projectDir,
		ProjectRoot:   ws.ProjectRoot,
		ProjectConfig: ws.Merged,
		TargetRootfs:  target,
		Global:        isGlobal,
	})
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("%s: build exited %d", tool, exit)
	}
	// Persist script + scope (first-run or --script override). Skip for --rerun
	// without --script so we don't churn the recorded script on a refresh.
	if !buildRerun || buildScript != "" {
		def.BuildRootfs = target
		if buildRerun {
			def.BuildScript = buildScript
		} else {
			def.BuildScript = command + " " + strings.Join(arguments, " ")
		}
		if isGlobal {
			def.BuildScope = "global"
			def.BuildProjectRoot = ""
		} else {
			def.BuildScope = "project"
			def.BuildProjectRoot = ws.ProjectRoot
		}
		_ = cfg.InstallTool(tool, def)
	}
	return nil
}

// resolveRebuildScope picks the rootfs target for a --rerun. It trusts the
// scope recorded at build time (def.BuildScope) so a missing project rootfs
// can't silently overwrite the shared global artifact with project-merged
// config. Priority: --global > stored scope > legacy filesystem fallback.
func resolveRebuildScope(tool string, def config.ToolDefinition, projectRoot string, forceGlobal bool) (string, bool, error) {
	if forceGlobal {
		return runtime.GlobalBuildRootfs(tool), true, nil
	}
	switch def.BuildScope {
	case "global":
		return runtime.GlobalBuildRootfs(tool), true, nil
	case "project":
		if def.BuildProjectRoot == "" {
			return "", false, errs.Configf("%s: build is project-scoped but no project root was recorded; run `silo build %s -- ...` again", tool, tool)
		}
		if projectRoot == "" || projectRoot != def.BuildProjectRoot {
			return "", false, errs.Configf("%s: build belongs to project %s; run from there, or pass --global to overwrite the shared artifact", tool, def.BuildProjectRoot)
		}
		return runtime.ProjectRootfs(projectRoot, tool), false, nil
	default:
		fmt.Fprintf(os.Stderr, "note: tool %q has no recorded build scope; run `silo build` to record it\n", tool)
		if def.BuildRootfs != "" {
			return runtime.GlobalBuildRootfs(tool), true, nil
		}
		if projectRoot != "" {
			projectTarget := runtime.ProjectRootfs(projectRoot, tool)
			if _, err := os.Stat(projectTarget); err == nil {
				return projectTarget, false, nil
			}
		}
		return runtime.GlobalBuildRootfs(tool), true, nil
	}
}

func removeBuild(target string) error {
	if _, err := os.Stat(target); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "No rootfs to remove at %s\n", target)
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("failed to remove %s: %w", target, err)
	}
	fmt.Fprintf(os.Stderr, "Removed %s\n", target)
	return nil
}
