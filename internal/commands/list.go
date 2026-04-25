// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/tools"
)

var listAvailable bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed (or available) tools",
	RunE: func(_ *cobra.Command, _ []string) error {
		if listAvailable {
			return printAvailable()
		}
		return printInstalled()
	},
}

func init() {
	listCmd.Flags().BoolVar(&listAvailable, "available", false, "list registry entries instead of installed")
	addCommand(listCmd)
}

func printInstalled() error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	return renderInstalled(os.Stdout, cfg)
}

// renderInstalled writes the `silo list` table to w. Pulled out so tests can
// drive it with a hand-built GlobalConfig.
func renderInstalled(w io.Writer, cfg *config.GlobalConfig) error {
	if len(cfg.Tools) == 0 {
		_, err := fmt.Fprintln(w, "No tools installed. Try: silo install python")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tIMAGE\tPINNED\tSHIMS")
	names := make([]string, 0, len(cfg.Tools))
	for k := range cfg.Tools {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		t := cfg.Tools[name]
		shimNames := make([]string, 0, len(t.Shims))
		for _, s := range t.Shims {
			shimNames = append(shimNames, s.String())
		}
		// PINNED indicates whether shim invocations of this tool always
		// dispatch into silo (yes — `silo install`) or fall through to the
		// next instance on PATH outside projects that claim it (no — `silo
		// sync`-installed). See `silo pin` / `silo unpin` to flip.
		pinned := "no"
		if t.PinnedGlobally {
			pinned = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, t.Image, pinned, strings.Join(shimNames, ", "))
	}
	return tw.Flush()
}

func printAvailable() error {
	entries, err := tools.Entries()
	if err != nil {
		return err
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tSTATUS\tDESCRIPTION")
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		status := "available"
		if _, ok := cfg.Tools[name]; ok {
			status = "installed"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, status, entries[name].Description)
	}
	return w.Flush()
}
