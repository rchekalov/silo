// SPDX-License-Identifier: Apache-2.0

// Binary silo is the command-line entry point. Before handing off to cobra,
// this file handles two bits of sugar the Rust implementation pioneered:
//
//  1. argv[0] shim dispatch — when the binary is invoked via a symlink in
//     ~/.silo/bin/ (e.g. `python` → silo), we resolve the shim to its tool
//     and transform into `silo run <tool> --shim <shim> -- <args>`.
//  2. Tool shorthand — `silo python foo.py` is rewritten to
//     `silo run python -- foo.py` before cobra ever sees it.
//
// Everything after `--` is stripped and forwarded via _SILO_PASSTHROUGH
// (delimiter \x1F) so cobra doesn't try to parse the inner command's flags.
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

	// Tool shorthand: `silo python foo.py` → `silo run python -- foo.py`
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
	if len(remaining) > 0 {
		args = append(args, "--")
		args = append(args, remaining...)
	}
	passthrough := transformArgs(&args, cfg)
	if len(passthrough) > 0 {
		_ = os.Setenv("_SILO_PASSTHROUGH", strings.Join(passthrough, "\x1F"))
	}
	os.Args = append([]string{"silo"}, args...)
	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	return true
}

// transformArgs mutates `args` in place:
//   - If the first arg is an installed tool (not reserved), wrap it as `run <tool> -- <rest>`.
//   - If it's a shim for an installed tool, wrap as `run <tool> --shim <shim> -- <rest>`.
//   - `shim <tool> <action> ...` is rearranged to `shim <action> <tool> ...`.
//   - Everything after `--` is returned as passthrough; the `--` and tail are stripped.
func transformArgs(args *[]string, cfg *config.GlobalConfig) []string {
	if cfg != nil && len(*args) > 0 {
		first := (*args)[0]
		if !reservedNames[first] && !strings.HasPrefix(first, "-") {
			if _, ok := cfg.Tools[first]; ok {
				rest := (*args)[1:]
				wrap := []string{"run", first}
				if len(rest) > 0 {
					wrap = append(wrap, "--")
					wrap = append(wrap, rest...)
				}
				*args = wrap
			} else if toolName, _ := cfg.ResolveShim(first); toolName != "" {
				rest := (*args)[1:]
				wrap := []string{"run", toolName, "--shim", first}
				if len(rest) > 0 {
					wrap = append(wrap, "--")
					wrap = append(wrap, rest...)
				}
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

	// Strip trailing `-- <passthrough>`.
	var passthrough []string
	for i, a := range *args {
		if a == "--" {
			passthrough = append([]string{}, (*args)[i+1:]...)
			*args = (*args)[:i]
			break
		}
	}
	return passthrough
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
