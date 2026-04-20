// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/tools"
	"github.com/spf13/cobra"
)

var listAvailable bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed (or available) tools",
	RunE: func(cmd *cobra.Command, args []string) error {
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
	if len(cfg.Tools) == 0 {
		fmt.Println("No tools installed. Try: silo install python")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tIMAGE\tSHIMS")
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
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, t.Image, strings.Join(shimNames, ", "))
	}
	return w.Flush()
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
