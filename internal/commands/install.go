// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"io"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/shim"
	"github.com/rchekalov/silo/internal/tools"
	"github.com/spf13/cobra"
)

var (
	installVersion string
	installImage   string
	installShims   []string
	installForce   bool
)

var installCmd = &cobra.Command{
	Use:   "install <tool>[@<version>]",
	Short: "Install a tool into ~/.silo",
	Long: `Install a tool into the global inventory at ~/.silo.

Version is specified inline with @:
  silo install python@3.12
  silo install node

If the tool is already installed, install refuses unless --force is set.
To pin a different version for the current project, use 'silo use' instead.`,
	Args: cobra.ExactArgs(1),
	RunE: runInstall,
}

func init() {
	installCmd.Flags().StringVar(&installVersion, "version", "", "specific version tag (deprecated: prefer <tool>@<version>)")
	installCmd.Flags().StringVar(&installImage, "image", "", "custom OCI image reference")
	installCmd.Flags().StringSliceVar(&installShims, "shim", nil, "custom shim names (comma-separated)")
	installCmd.Flags().BoolVar(&installForce, "force", false, "force global reinstall")
	addCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	toolName, version, err := tools.ParseSpec(args[0])
	if err != nil {
		return err
	}
	if version != "" && installVersion != "" && installVersion != version {
		return fmt.Errorf("conflicting versions: spec %q vs --version %q", args[0], installVersion)
	}
	if version == "" {
		version = installVersion
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	if _, ok := cfg.Tools[toolName]; ok && !installForce {
		return fmt.Errorf("%w; pass --force to reinstall, or run `silo use %s@<version>` to pin a different version in this project",
			errs.ToolAlreadyInstalledError(toolName), toolName)
	}

	e := engine.NewContainerEngine(cfg)
	installer := &tools.Installer{
		Config:        cfg,
		Shims:         shim.NewManager(""),
		Prompter:      Prompter,
		EnsureRuntime: e.EnsureRuntime,
		PullImage:     e.PullImage,
		RunCaptured:   captureRunAdapter(e),
		RunSetup: bakeAdapter(e),
	}

	_, wasInstalled := cfg.Tools[toolName]
	_, err = installer.Install(tools.InstallOptions{
		Name:    toolName,
		Version: version,
		Image:   installImage,
		Shims:   installShims,
		Force:   installForce,
	})
	if err != nil {
		return err
	}
	verb := "Installed"
	if wasInstalled {
		verb = "Reinstalled"
	}
	fmt.Printf("%s %q.\n", verb, toolName)
	return nil
}

// bakeAdapter adapts engine.ContainerEngine.RunSetup to tools.BakeFunc. The
// adapter is shared between `silo install` (global bakes of registry postInstall)
// and `silo sync` (project-scoped bakes for .siloconf postInstall overrides).
func bakeAdapter(e *engine.ContainerEngine) tools.BakeFunc {
	return func(name string, tool config.ToolDefinition, cmd string, arguments []string, target string, global bool) (int32, error) {
		return e.RunSetup(engine.RunSetupOptions{
			ToolName:     name,
			Tool:         tool,
			Command:      cmd,
			Arguments:    arguments,
			TargetRootfs: target,
			Global:       global,
		})
	}
}

// captureRunAdapter adapts engine.ContainerEngine.RunEphemeral to tools.CaptureRunFunc.
func captureRunAdapter(e *engine.ContainerEngine) tools.CaptureRunFunc {
	return func(toolName string, tool config.ToolDefinition, command string, arguments []string, out io.Writer) (int32, error) {
		return e.RunEphemeral(engine.RunEphemeralOptions{
			ToolName:    toolName,
			Tool:        tool,
			Command:     command,
			Arguments:   arguments,
			Interactive: false,
			Stdout:      out,
		})
	}
}
