// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Reclaim global disk: orphan images, cold rootfs entries, per-tool caches",
	Long: `One-shot global cleanup. Equivalent to:

  silo cache gc --images --tool-caches

Applies the policy from .siloconf (or the default) to evict cold rootfs cache
entries and per-tool package caches, and sweeps orphan OCI image blobs.

For project-scoped cleanup (rootfs + per-tool caches for one project's tools),
use 'silo clean' instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		gcImages = true
		gcToolCaches = true
		fmt.Fprintln(os.Stderr, "Pruning: rootfs cache (LRU + age), per-tool caches, orphan OCI layers.")
		return runCacheGC(cmd, args)
	},
}

func init() {
	pruneCmd.Flags().BoolVar(&gcDryRun, "dry-run", false, "print the plan without evicting")
	addCommand(pruneCmd)
}
