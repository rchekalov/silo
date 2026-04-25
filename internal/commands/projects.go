// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/runtime"
)

var (
	projectsLsJSON     bool
	projectsLsOrphaned bool
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Inspect silo's per-machine project state under ~/.silo/projects/",
}

var projectsLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List projects silo has cached state for (auto-baked rootfs, last-used path)",
	Long: `Walks ~/.silo/projects and prints one row per project. Status is "ok" if
the recorded path still exists on disk, "orphaned" otherwise. Use
'silo clean --orphaned' to reap orphans plus any baked rootfs they
were the last referrers of.`,
	RunE: runProjectsLs,
}

func init() {
	projectsLsCmd.Flags().BoolVar(&projectsLsJSON, "json", false, "machine-readable output")
	projectsLsCmd.Flags().BoolVar(&projectsLsOrphaned, "orphaned", false, "only show entries whose path is missing")
	projectsCmd.AddCommand(projectsLsCmd)
	addCommand(projectsCmd)
}

type projectRow struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Tools      []string `json:"tools,omitempty"`
	SizeBytes  uint64   `json:"sizeBytes"`
	LastUsedAt string   `json:"lastUsedAt,omitempty"`
	Status     string   `json:"status"`
}

func runProjectsLs(_ *cobra.Command, _ []string) error {
	projects, err := runtime.ListProjects()
	if err != nil {
		return err
	}

	rows := make([]projectRow, 0, len(projects))
	for _, p := range projects {
		status := "ok"
		if _, err := os.Stat(p.Meta.Path); err != nil {
			status = "orphaned"
		}
		if projectsLsOrphaned && status != "orphaned" {
			continue
		}
		size := dirSize(runtime.ProjectStateDir(p.ID))
		// Add the size of every baked rootfs this project references — those
		// blobs aren't *exclusively* this project's, but `silo projects ls`
		// is about visibility, not accounting, and showing the dependency's
		// size makes the output a useful disk-usage summary.
		for _, h := range p.Meta.ToolToRecipe {
			size += dirSize(runtime.BakedDir(h))
		}
		var lastUsed string
		if !p.Meta.LastUsedAt.IsZero() {
			lastUsed = p.Meta.LastUsedAt.Format(time.RFC3339)
		}
		rows = append(rows, projectRow{
			ID:         p.ID,
			Path:       p.Meta.Path,
			Tools:      append([]string(nil), p.Meta.Tools...),
			SizeBytes:  size,
			LastUsedAt: lastUsed,
			Status:     status,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })

	if projectsLsJSON {
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	if len(rows) == 0 {
		if projectsLsOrphaned {
			fmt.Println("No orphaned projects.")
		} else {
			fmt.Println("No projects with cached state. Run `silo sync` in a project to populate ~/.silo/projects/.")
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tTOOLS\tSIZE\tLAST USED\tSTATUS")
	for _, r := range rows {
		lastUsed := r.LastUsedAt
		if lastUsed == "" {
			lastUsed = "-"
		}
		tools := strings.Join(r.Tools, ",")
		if tools == "" {
			tools = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%.1f MiB\t%s\t%s\n",
			r.Path, tools, float64(r.SizeBytes)/1024/1024, lastUsed, r.Status)
	}
	return w.Flush()
}
