// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
)

// ApplyProjectPostInstall bakes project-level postInstall steps for `name`
// into a content-addressed rootfs at ~/.silo/baked/<recipe-hash>/rootfs.ext4
// (where recipe-hash = sha256 of the joined steps). It's idempotent: if a
// rootfs already exists at that path, the call is a no-op.
//
// When extraSteps is empty the call is a no-op and recipeHash is "". The
// returned bool is true iff a fresh bake actually ran.
//
// def is the tool's effective definition (registry + project overrides
// already applied). extraSteps is the project contribution only — the
// registry base is not re-run because the global rootfs produced by
// `silo install` already contains it, and engine.ephemeral seeds the bake
// VM from that global rootfs when present.
//
// On the first call after upgrade, any pre-0.5.0 <projectRoot>/.silo/<tool>/
// directories that look like project-bakes (carry the sha256 sidecar) are
// removed; see runtime.MigrateLegacyProjectDir.
func ApplyProjectPostInstall(
	run BakeFunc,
	name string,
	def config.ToolDefinition,
	extraSteps []string,
	projectRoot string,
) (bool, string, error) {
	if len(extraSteps) == 0 {
		return false, "", nil
	}
	if projectRoot == "" {
		return false, "", fmt.Errorf("project-level postInstall for %s requires a project root (.siloconf)", name)
	}
	if err := runtime.MigrateLegacyProjectDir(projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: legacy .silo migration failed: %v\n", err)
	}

	recipeHash := hashSteps(extraSteps)
	target := runtime.BakedRootfs(recipeHash)
	if _, err := os.Stat(target); err == nil {
		return false, recipeHash, nil
	}

	if _, err := BakeTool(run, BakeOptions{
		Name:        name,
		Def:         def,
		Steps:       extraSteps,
		Target:      target,
		Scope:       "project",
		ProjectRoot: projectRoot,
	}); err != nil {
		return false, "", err
	}
	if err := writeBakeManifest(recipeHash, name, def.Image, extraSteps); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write bake manifest at %s: %v\n", runtime.BakedManifest(recipeHash), err)
	}
	return true, recipeHash, nil
}

// ApplyProjectFullBake bakes the *full* postInstall chain (registry base +
// project overrides, as already merged in def.PostInstall) into a
// content-addressed rootfs at ~/.silo/baked/<recipe-hash>/rootfs.ext4,
// starting cold from the pinned image rather than seeding from the global
// rootfs.
//
// Needed when a project pins a different image version than the globally
// installed one: the global rootfs was produced against the wrong toolchain
// and can't be an overlay base. The recipe-hash mixes in def.Image so a
// `silo use python@3.11` after a prior @3.12 sync produces a different hash
// and triggers a fresh bake.
func ApplyProjectFullBake(
	run BakeFunc,
	name string,
	def config.ToolDefinition,
	allSteps []string,
	projectRoot string,
) (bool, string, error) {
	if len(allSteps) == 0 {
		return false, "", nil
	}
	if projectRoot == "" {
		return false, "", fmt.Errorf("project full bake for %s requires a project root (.siloconf)", name)
	}
	if err := runtime.MigrateLegacyProjectDir(projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: legacy .silo migration failed: %v\n", err)
	}

	recipeSteps := append([]string{"image=" + def.Image}, allSteps...)
	recipeHash := hashSteps(recipeSteps)
	target := runtime.BakedRootfs(recipeHash)
	if _, err := os.Stat(target); err == nil {
		return false, recipeHash, nil
	}

	if _, err := BakeTool(run, BakeOptions{
		Name:        name,
		Def:         def,
		Steps:       allSteps,
		Target:      target,
		Scope:       "project",
		ProjectRoot: projectRoot,
		FromScratch: true,
	}); err != nil {
		return false, "", err
	}
	if err := writeBakeManifest(recipeHash, name, def.Image, allSteps); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write bake manifest at %s: %v\n", runtime.BakedManifest(recipeHash), err)
	}
	return true, recipeHash, nil
}

// hashSteps returns the SHA-256 of `\n`-joined steps, as hex. The separator
// is newline (not `&&`) so whitespace inside a step cannot collide with
// another step's boundary — keeps the hash stable against step reorderings
// that happen to produce the same joined script.
func hashSteps(steps []string) string {
	sum := sha256.Sum256([]byte(strings.Join(steps, "\n")))
	return hex.EncodeToString(sum[:])
}

// bakeManifest is the small JSON sidecar at ~/.silo/baked/<hash>/manifest.json
// describing what produced the rootfs. Used by `silo projects` and `silo
// clean` to render human-readable info; not load-bearing for correctness
// (the directory name is the canonical identity).
type bakeManifest struct {
	Tool      string    `json:"tool"`
	Image     string    `json:"image"`
	Steps     []string  `json:"steps"`
	CreatedAt time.Time `json:"createdAt"`
}

func writeBakeManifest(recipeHash, tool, image string, steps []string) error {
	m := bakeManifest{
		Tool:      tool,
		Image:     image,
		Steps:     append([]string(nil), steps...),
		CreatedAt: time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(runtime.BakedManifest(recipeHash), raw, 0o644)
}
