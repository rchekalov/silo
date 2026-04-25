// SPDX-License-Identifier: Apache-2.0

// Binary silo is the command-line entry point. Before handing off to cobra,
// this file handles three bits of sugar:
//
//  1. argv[0] shim dispatch — when the binary is invoked via a symlink in
//     ~/.silo/bin/ (e.g. `python` → silo), we resolve the shim to its tool
//     and transform into `silo run <tool> --shim <shim> <args...>`.
//  2. Tool shorthand — `silo python foo.py` is rewritten to
//     `silo run python foo.py` before cobra ever sees it.
//  3. Docker-style positional split for `silo run` and `silo build` — silo
//     flags appear before the tool name; everything after the tool is the
//     inner command. Known silo flags appearing after the tool are hoisted
//     in front of it so `silo build node --remove` keeps working.
//
// The pass-through tail is forwarded via _SILO_PASSTHROUGH (delimiter \x1F)
// so cobra doesn't try to parse the inner command's flags. The legacy `--`
// separator is still accepted: if it appears anywhere in argv, we use the
// pre-split strip-everything-after-`--` path.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/rchekalov/silo/internal/commands"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/version"
)

var reservedNames = map[string]bool{
	"install": true, "uninstall": true, "list": true, "run": true,
	"shell": true, "status": true, "setup": true, "rebuild": true,
	"cache": true, "config": true, "reset": true, "lsp": true,
	"ide": true, "init": true, "shim": true,
	// 0.5.0 additions:
	"use": true, "unuse": true, "sync": true, "apply": true,
	"build": true, "doctor": true, "current": true, "prune": true,
	// Cobra-added:
	"help": true, "completion": true,
	// version flag:
	"--help": true, "-h": true, "--version": true, "version": true,
}

func main() {
	resetTerminalIfNeeded()

	argv0 := filepath.Base(os.Args[0])

	// Shim dispatch: when invoked as ~/.silo/bin/<shim>, resolve and re-enter.
	if argv0 != "silo" && argv0 != "" {
		if handled := tryShimDispatch(argv0); handled {
			return
		}
		// Otherwise fall through as normal silo.
	}

	// --version shortcut before any transforms.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("silo version %s\n", version.Version)
		return
	}

	args := append([]string(nil), os.Args[1:]...)

	// Tool shorthand: `silo python foo.py` → `silo run python foo.py`
	cfg, _ := config.LoadGlobalConfig()
	passthrough := transformArgs(&args, cfg)
	if len(passthrough) > 0 {
		_ = os.Setenv("_SILO_PASSTHROUGH", strings.Join(passthrough, "\x1F"))
	}

	// Hand rewritten argv to cobra.
	os.Args = append([]string{os.Args[0]}, args...)

	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// tryShimDispatch returns true if argv[0] was resolved as a shim and handled.
func tryShimDispatch(shim string) bool {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return false
	}
	toolName, _ := cfg.ResolveShim(shim)
	if toolName == "" {
		return false
	}
	remaining := os.Args[1:]
	args := []string{"run", toolName, "--shim", shim}
	args = append(args, remaining...)
	passthrough := transformArgs(&args, cfg)
	if len(passthrough) > 0 {
		_ = os.Setenv("_SILO_PASSTHROUGH", strings.Join(passthrough, "\x1F"))
	}
	// Mark this invocation as having entered silo through a PATH shim (vs. the
	// user explicitly typing `silo run ...`). The run command consults this to
	// decide whether to fall through to the next instance on PATH when no
	// project claims the tool and it isn't globally pinned.
	_ = os.Setenv("_SILO_SHIM_DISPATCH", "1")
	os.Args = append([]string{"silo"}, args...)
	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	return true
}

// Subcommand → known silo flags. Imported from the commands package so the
// tables can't drift from the cobra Flags() registrations.
var (
	subcmdValueFlags = map[string][]string{
		"run":   commands.RunValueFlags,
		"build": commands.BuildValueFlags,
	}
	subcmdBoolFlags = map[string][]string{
		"run":   commands.RunBoolFlags,
		"build": commands.BuildBoolFlags,
	}
)

// transformArgs mutates `args` in place:
//   - If the first arg is an installed tool (not reserved), wrap it as `run <tool> <rest>`.
//   - If it's a shim for an installed tool, wrap as `run <tool> --shim <shim> <rest>`.
//   - `shim <tool> <action> ...` is rearranged to `shim <action> <tool> ...`.
//   - For `run` / `build`, hoist known silo flags to the front of the tool
//     positional and treat everything after the tool as pass-through.
//   - The legacy `-- <args>` form still works: if `--` appears anywhere, we
//     skip the positional split and just strip-after-`--`.
func transformArgs(args *[]string, cfg *config.GlobalConfig) []string {
	if cfg != nil && len(*args) > 0 {
		first := (*args)[0]
		if !reservedNames[first] && !strings.HasPrefix(first, "-") {
			if _, ok := cfg.Tools[first]; ok {
				rest := (*args)[1:]
				wrap := append([]string{"run", first}, rest...)
				*args = wrap
			} else if toolName, _ := cfg.ResolveShim(first); toolName != "" {
				rest := (*args)[1:]
				wrap := append([]string{"run", toolName, "--shim", first}, rest...)
				*args = wrap
			}
		}
	}

	// `silo shim <tool> <action> ...` → `silo shim <action> <tool> ...`
	if len(*args) >= 3 && (*args)[0] == "shim" {
		action := (*args)[2]
		if action == "add" || action == "remove" || action == "list" {
			(*args)[1], (*args)[2] = action, (*args)[1]
		}
	}

	// Legacy `--` form: strip and return as passthrough. Wins over the new
	// positional split — if the user typed `--`, honour it verbatim.
	for i, a := range *args {
		if a == "--" {
			passthrough := append([]string{}, (*args)[i+1:]...)
			*args = (*args)[:i]
			return passthrough
		}
	}

	// Docker-style positional split for `run` / `build`.
	if len(*args) >= 1 {
		subcmd := (*args)[0]
		if _, ok := subcmdValueFlags[subcmd]; ok {
			return splitPositional(args, subcmd)
		}
	}
	return nil
}

// splitPositional walks args[1:], hoists known silo flags (and their value
// args) to the front, locates the tool name, and treats everything after the
// tool as pass-through. On a moved-flag breakage (e.g. `silo run python --timing`)
// it emits a one-shot stderr hint.
func splitPositional(args *[]string, subcmd string) []string {
	valueFlags := flagSet(subcmdValueFlags[subcmd])
	boolFlags := flagSet(subcmdBoolFlags[subcmd])

	rest := (*args)[1:]
	var siloFlags []string
	var leftover []string
	i := 0
	for i < len(rest) {
		a := rest[i]
		if a == "-h" || a == "--help" {
			siloFlags = append(siloFlags, a)
			i++
			continue
		}
		name, hasEq := flagName(a)
		if name != "" {
			if _, ok := valueFlags[name]; ok {
				if hasEq || i+1 >= len(rest) {
					siloFlags = append(siloFlags, a)
					i++
				} else {
					siloFlags = append(siloFlags, a, rest[i+1])
					i += 2
				}
				continue
			}
			if _, ok := boolFlags[name]; ok {
				siloFlags = append(siloFlags, a)
				i++
				continue
			}
			// Unknown flag → not silo's; falls into leftover.
		}
		leftover = append(leftover, a)
		i++
	}

	// Find tool positional in leftover (first non-flag).
	toolIdx := -1
	for j, a := range leftover {
		if !strings.HasPrefix(a, "-") {
			toolIdx = j
			break
		}
	}
	if toolIdx < 0 {
		// No tool — leave args alone (e.g. `silo build --all --rerun`).
		return nil
	}
	tool := leftover[toolIdx]
	// Anything in leftover before the tool is non-silo flags before the tool;
	// keep them with the tool so they precede pass-through (rare, e.g. an
	// unknown flag the user typed by mistake — cobra will surface the error).
	prefix := leftover[:toolIdx]
	var passthrough []string
	if toolIdx+1 < len(leftover) {
		passthrough = leftover[toolIdx+1:]
	}

	// One-shot hint: if the pass-through contains a token matching a known
	// silo flag, the user almost certainly meant it as a silo flag.
	maybeHintMovedFlag(subcmd, passthrough, valueFlags, boolFlags)

	newArgs := []string{subcmd}
	newArgs = append(newArgs, siloFlags...)
	newArgs = append(newArgs, prefix...)
	newArgs = append(newArgs, tool)
	*args = newArgs
	return passthrough
}

// flagName returns the canonical flag name (without leading dashes, without
// `=value` tail) for a token like `--shim`, `--shim=pip`, or `-h`. Returns
// empty string for non-flag tokens.
func flagName(a string) (name string, hasEq bool) {
	if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
		return "", false
	}
	s := strings.TrimLeft(a, "-")
	if eq := strings.IndexByte(s, '='); eq >= 0 {
		return s[:eq], true
	}
	return s, false
}

func flagSet(names []string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

func maybeHintMovedFlag(subcmd string, passthrough []string, valueFlags, boolFlags map[string]struct{}) {
	for _, a := range passthrough {
		name, _ := flagName(a)
		if name == "" {
			continue
		}
		if _, ok := valueFlags[name]; ok {
			emitMovedFlagHint(subcmd, a)
			return
		}
		if _, ok := boolFlags[name]; ok {
			emitMovedFlagHint(subcmd, a)
			return
		}
	}
}

func emitMovedFlagHint(subcmd, flag string) {
	fmt.Fprintf(os.Stderr,
		"note: %q now goes to the inner command. Place silo flags before the tool name (silo %s %s <tool> ...).\n",
		flag, subcmd, flag)
}

// resetTerminalIfNeeded restores cooked mode on stdin if a prior silo run
// crashed mid-raw-mode. No-op when stdin isn't a TTY.
func resetTerminalIfNeeded() {
	fd := int(os.Stdin.Fd())
	tios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return
	}
	wantL := uint64(unix.ICANON | unix.ECHO | unix.ECHOE | unix.ISIG)
	wantI := uint64(unix.ICRNL)
	wantO := uint64(unix.OPOST)
	if tios.Lflag&wantL == wantL && tios.Iflag&wantI == wantI && tios.Oflag&wantO == wantO {
		return
	}
	tios.Lflag |= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ISIG
	tios.Iflag |= unix.ICRNL
	tios.Oflag |= unix.OPOST
	_ = unix.IoctlSetTermios(fd, unix.TIOCSETA, tios)
}
