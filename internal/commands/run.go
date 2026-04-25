// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/runtime"
)

var runCmd = &cobra.Command{
	Use:   "run <tool> [args...]",
	Short: "Run a command in an ephemeral container",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

var (
	runShim   string
	runTiming bool
)

// Flag tables consumed by cmd/silo/main.go to split argv into silo flags +
// tool name + pass-through. Keep in sync with the Flags() registrations below.
var (
	RunValueFlags = []string{"shim"}
	RunBoolFlags  = []string{"timing"}
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

func runRun(_ *cobra.Command, args []string) error {
	passthrough := passthroughArgs()
	total := time.Now()

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	tool, def, resolvedShim, err := resolveToolOrShim(cfg, args[0])
	if err != nil {
		return err
	}

	command := tool
	switch {
	case runShim != "":
		command = runShim
	case resolvedShim != "":
		command = resolvedShim
	}

	if runShim == "" && resolvedShim == "" {
		command, passthrough = applySiblingShim(cfg, tool, command, passthrough, os.Stderr)
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

	// Pyenv-style fall-through: when this invocation arrived via a PATH shim
	// (~/.silo/bin/<cmd> → silo run), the user typed `<cmd>` expecting their
	// system tool — silo only got in the way because its bin is on PATH. If no
	// project claims this tool and it isn't globally pinned (`silo install`),
	// strip ~/.silo/bin/ from PATH and exec the next instance transparently.
	// Direct invocations (`silo run <tool>` / `silo <tool>` shorthand) skip
	// this branch — they are explicit and must run inside silo or error out.
	if os.Getenv("_SILO_SHIM_DISPATCH") == "1" {
		projectClaims := mergedCfg != nil && mergedCfg.Claims(tool)
		if !projectClaims && !def.PinnedGlobally {
			return execNextOnPath(command, passthrough)
		}
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

// execNextOnPath replaces the current silo process with the next instance of
// `command` found on PATH after stripping ~/.silo/bin/. This is the
// fall-through path for shim invocations of tools that no project claims and
// that aren't globally pinned — silo gets out of the way and lets the user's
// system tool run as if silo's shim weren't on PATH.
//
// The exec'd process inherits an environment where ~/.silo/bin/ has been
// removed from PATH, matching pyenv's behavior for non-pyenv-managed
// commands. Without that, a fork from the exec'd process (e.g. `npm` running
// `node`) would re-enter silo's shim and bounce again.
func execNextOnPath(command string, args []string) error {
	next, env, err := resolveFallThrough(command, runtime.ShimBin(), os.Getenv("PATH"), os.Environ())
	if err != nil {
		return err
	}
	fullArgs := append([]string{command}, args...)
	return syscall.Exec(next, fullArgs, env)
}

// resolveFallThrough is the side-effect-free core of execNextOnPath: given a
// command, the silo shim dir to strip, the inbound PATH, and the inbound
// environment, it returns (path-to-next-instance, env-to-pass-to-exec, err).
// Pulled out of execNextOnPath so unit tests can drive it without touching
// process-global state or actually exec'ing.
//
// `inboundEnv` is the environ slice as os.Environ() returns it ("KEY=VAL").
// The returned env strips both PATH (replaced with the filtered version) and
// _SILO_SHIM_DISPATCH (so a re-entrant silo invocation doesn't inherit the
// shim marker from a parent process).
func resolveFallThrough(command, shimBin, inboundPATH string, inboundEnv []string) (next string, env []string, err error) {
	parts := filepath.SplitList(inboundPATH)
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == shimBin {
			continue
		}
		filtered = append(filtered, p)
	}
	filteredPATH := strings.Join(filtered, string(filepath.ListSeparator))

	// exec.LookPath consults the process's PATH, so swap it in for the
	// duration of the lookup and restore on the way out. Tests using this
	// helper still mutate process state via t.Setenv, but the swap stays
	// localized.
	origPATH := os.Getenv("PATH")
	_ = os.Setenv("PATH", filteredPATH)
	next, lookupErr := exec.LookPath(command)
	_ = os.Setenv("PATH", origPATH)
	if lookupErr != nil {
		return "", nil, fmt.Errorf(
			"silo: %q is not claimed by any project (.siloconf) and not globally pinned, and not found on PATH after stripping %s.\n"+
				"  • To pin it everywhere: silo install %s\n"+
				"  • To use it inside this project: silo use %s && silo sync",
			command, shimBin, command, command,
		)
	}

	out := make([]string, 0, len(inboundEnv)+1)
	for _, kv := range inboundEnv {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		if strings.HasPrefix(kv, "_SILO_SHIM_DISPATCH=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PATH="+filteredPATH)
	return next, out, nil
}

// applySiblingShim implements Docker-style entrypoint override when the first
// passthrough arg is a known shim. If it's a shim of the same tool, promote it
// to command and shift it off passthrough. If it's a shim of a different tool
// only, write a one-line hint to stderrW and leave args unchanged.
func applySiblingShim(
	cfg *config.GlobalConfig,
	tool, command string,
	passthrough []string,
	stderrW io.Writer,
) (string, []string) {
	if len(passthrough) == 0 {
		return command, passthrough
	}
	arg0 := passthrough[0]
	if arg0 == "" || arg0 == command || arg0 == tool || strings.HasPrefix(arg0, "-") {
		return command, passthrough
	}
	matches := cfg.ResolveShimAll(arg0)
	if len(matches) == 0 {
		return command, passthrough
	}
	for _, m := range matches {
		if m == tool {
			return arg0, passthrough[1:]
		}
	}
	rest := ""
	if len(passthrough) > 1 {
		rest = " " + strings.Join(passthrough[1:], " ")
	}
	fmt.Fprintf(stderrW,
		"silo: hint: %q is a shim of %s, not %s. Did you mean: silo run %s%s\n",
		arg0, strings.Join(matches, ", "), tool, arg0, rest)
	return command, passthrough
}
