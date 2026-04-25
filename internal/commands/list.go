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
	"github.com/rchekalov/silo/internal/runtime"
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
	imgState, err := runtime.LoadImageState()
	if err != nil {
		// state.json is best-effort metadata for the additional rows;
		// failure to parse should not break `silo list`.
		fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", runtime.ImageState(), err)
		imgState = nil
	}
	registry, err := tools.Entries()
	if err != nil {
		// Same rationale: registry lookup helps tag project-pinned images
		// with a tool name; if it fails we fall back to the image ref.
		fmt.Fprintf(os.Stderr, "warning: could not load registry: %v\n", err)
		registry = nil
	}
	return renderInstalled(os.Stdout, cfg, imgState, registry)
}

// renderInstalled writes the `silo list` table to w. Pulled out so tests can
// drive it with hand-built inputs.
//
// The table includes:
//   - Global tools from ~/.silo/config.yaml (PINNED yes/no per `silo pin`).
//   - Additional images present in ~/.silo/state.json (the OCI image index)
//     that aren't a registered global tool's image — these are project-only
//     pins from .siloconf, shown with PINNED=project so the user has a single
//     inventory of every cached image they've pulled. pyenv-style.
type listRow struct {
	tool, image, pinned, shims string
}

func renderInstalled(w io.Writer, cfg *config.GlobalConfig, imgState map[string]runtime.ImageStateEntry, registry map[string]tools.RegistryEntry) error {
	if len(cfg.Tools) == 0 && len(imgState) == 0 {
		_, err := fmt.Fprintln(w, "No tools installed. Try: silo install python")
		return err
	}

	var rows []listRow

	globallyPulled := make(map[string]struct{}, len(cfg.Tools))
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
		rows = append(rows, listRow{tool: name, image: t.Image, pinned: pinned, shims: strings.Join(shimNames, ", ")})
		globallyPulled[t.Image] = struct{}{}
	}

	extra := projectPinnedRows(imgState, registry, globallyPulled)
	rows = append(rows, extra...)

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tIMAGE\tPINNED\tSHIMS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.tool, r.image, r.pinned, r.shims)
	}
	return tw.Flush()
}

// projectPinnedRows returns a stable-ordered list of rows for images present
// in the OCI image index but not registered as a global tool's image. The
// tool column is derived from the registry by matching the image repo (the
// part before the tag) against each registry entry's default image; falls
// back to the last path segment of the repo if no registry match exists.
func projectPinnedRows(imgState map[string]runtime.ImageStateEntry, registry map[string]tools.RegistryEntry, globallyPulled map[string]struct{}) []listRow {
	if len(imgState) == 0 {
		return nil
	}
	// Multiple registry tools can share an image repo (e.g. `node`, `claude-code`,
	// and `playwright` all base on `docker.io/library/node`). Track every
	// candidate per repo so we can disambiguate by tag below.
	repoToCandidates := make(map[string][]string, len(registry))
	for name, ent := range registry {
		repo := imageRepo(ent.Image)
		if repo == "" {
			continue
		}
		repoToCandidates[repo] = append(repoToCandidates[repo], name)
	}
	for _, names := range repoToCandidates {
		sort.Strings(names) // deterministic tie-break
	}

	refs := make([]string, 0, len(imgState))
	for ref := range imgState {
		if _, isGlobal := globallyPulled[ref]; isGlobal {
			continue
		}
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	out := make([]listRow, 0, len(refs))
	for _, ref := range refs {
		repo := imageRepo(ref)
		tag := imageTag(ref)
		toolName := pickToolForImage(repoToCandidates[repo], registry, ref, tag)
		if toolName == "" {
			toolName = repoBase(repo)
		}
		var shimNames []string
		if ent, ok := registry[toolName]; ok {
			shimNames = make([]string, 0, len(ent.Shims))
			for _, s := range ent.Shims {
				shimNames = append(shimNames, s.String())
			}
		}
		out = append(out, listRow{
			tool:   toolName,
			image:  ref,
			pinned: "project",
			shims:  strings.Join(shimNames, ", "),
		})
	}
	return out
}

// pickToolForImage chooses the most appropriate registry tool for a cached
// image when several tools share the same image repo. Preference order:
//
//  1. A tool whose `versions:` list contains the cached tag — this is the
//     canonical runtime that owns the version (`node:18-slim` → `node`,
//     not `playwright`).
//  2. A tool whose default image matches the cached ref exactly.
//  3. A tool that has a `versions:` list at all (canonical runtimes are
//     versioned; composite tools usually aren't).
//  4. The first candidate alphabetically (deterministic fallback).
func pickToolForImage(candidates []string, registry map[string]tools.RegistryEntry, ref, tag string) string {
	if len(candidates) == 0 {
		return ""
	}
	if tag != "" {
		for _, name := range candidates {
			for _, v := range registry[name].Versions {
				if v.Tag == tag {
					return name
				}
			}
		}
	}
	for _, name := range candidates {
		if registry[name].Image == ref {
			return name
		}
	}
	for _, name := range candidates {
		if len(registry[name].Versions) > 0 {
			return name
		}
	}
	return candidates[0]
}

// imageRepo returns the repository portion of an image reference (everything
// before the final `:` tag separator). Returns the original input if no tag
// is present. Digest references (`@sha256:...`) are treated as tags.
func imageRepo(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		// Avoid splitting on a port number in a registry hostname like
		// localhost:5000/foo:tag — the tag separator must come after the
		// last `/`.
		if slash := strings.LastIndex(ref, "/"); slash > i {
			return ref
		}
		return ref[:i]
	}
	return ref
}

// imageTag returns the tag portion of an image reference (everything after
// the final `:` separator that is itself after the last `/`). Returns "" if
// the reference is digest-pinned or carries no tag.
func imageTag(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ""
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return ""
	}
	if slash := strings.LastIndex(ref, "/"); slash > i {
		return ""
	}
	return ref[i+1:]
}

// repoBase returns the last `/`-separated segment of a repository path.
// Used to label cached images that don't match any registry entry.
func repoBase(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
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
