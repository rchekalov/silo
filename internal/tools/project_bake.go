// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
)

// ApplyProjectPostInstall bakes project-level postInstall steps for `name`
// into a project-scoped rootfs at <projectRoot>/.silo/<name>/rootfs.ext4.
// It is idempotent: a SHA-256 of the concatenated steps is stored next to
// the rootfs (script.sha256) and a match short-circuits the bake.
//
// When extraSteps is empty the call is a no-op. The returned bool indicates
// whether a bake actually ran (false = skipped because already up-to-date or
// no steps).
//
// def is the tool's effective definition (registry + project overrides
// already applied). extraSteps is the project contribution only — the
// registry base is not re-run because the global rootfs produced by
// `silo install` already contains it, and engine.ephemeral seeds the bake
// VM from that global rootfs when present.
func ApplyProjectPostInstall(
	run BakeFunc,
	name string,
	def config.ToolDefinition,
	extraSteps []string,
	projectRoot string,
) (bool, error) {
	if len(extraSteps) == 0 {
		return false, nil
	}
	if projectRoot == "" {
		return false, fmt.Errorf("project-level postInstall for %s requires a project root (.siloconf)", name)
	}

	target := runtime.ProjectRootfs(projectRoot, name)
	hashPath := target + ".sha256"
	want := hashSteps(extraSteps)

	if existing, err := os.ReadFile(hashPath); err == nil {
		if strings.TrimSpace(string(existing)) == want {
			if _, statErr := os.Stat(target); statErr == nil {
				return false, nil
			}
		}
	}

	if _, err := BakeTool(run, BakeOptions{
		Name:        name,
		Def:         def,
		Steps:       extraSteps,
		Target:      target,
		Scope:       "project",
		ProjectRoot: projectRoot,
	}); err != nil {
		return false, err
	}
	if err := os.WriteFile(hashPath, []byte(want+"\n"), 0o644); err != nil {
		// Sidecar write failed but the rootfs is good; warn and move on —
		// the next sync will re-bake, which is wasteful but not wrong.
		fmt.Fprintf(os.Stderr, "warning: could not record bake hash at %s: %v\n", hashPath, err)
	}
	return true, nil
}

// hashSteps returns the SHA-256 of `\n`-joined steps, as hex. The separator
// is newline (not `&&`) so whitespace inside a step cannot collide with
// another step's boundary — keeps the hash stable against step reorderings
// that happen to produce the same joined script.
func hashSteps(steps []string) string {
	sum := sha256.Sum256([]byte(strings.Join(steps, "\n")))
	return hex.EncodeToString(sum[:])
}
