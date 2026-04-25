// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ProjectMetaSchemaVersion is the version baked into freshly-written meta.json
// files. Bump on schema changes; readers should be tolerant of older versions.
const ProjectMetaSchemaVersion = 1

// ProjectMeta is the persisted per-project sidecar that lives at
// ~/.silo/projects/<id>/meta.json. It's small (kilobytes) and exists so silo
// can enumerate projects (`silo projects`), reap orphans (`silo clean
// --orphaned`), and resolve "which baked rootfs does this tool currently use"
// at runtime without re-deriving the recipe hash from scratch.
type ProjectMeta struct {
	SchemaVersion int               `json:"schemaVersion"`
	Path          string            `json:"path"`
	ProjectID     string            `json:"project_id,omitempty"`
	SiloconfHash  string            `json:"siloconfHash,omitempty"`
	Tools         []string          `json:"tools,omitempty"`
	ToolToRecipe  map[string]string `json:"tool_to_recipe,omitempty"`
	LastUsedAt    time.Time         `json:"lastUsedAt,omitempty"`
}

// ProjectID returns the per-project state-dir key. If explicitID is non-empty
// (set via `project_id:` in .siloconf), use it verbatim — that's the move-safe
// path. Otherwise fall back to a 16-char hex prefix of sha256(realpath). Path
// hashing is good-enough for cases where the user hasn't opted in to a stable
// ID; smart adoption (LoadOrCreateMeta) handles `mv` recovery for those.
func ProjectID(explicitID, projectRoot string) string {
	if explicitID != "" {
		return explicitID
	}
	real := projectRoot
	if r, err := filepath.EvalSymlinks(projectRoot); err == nil {
		real = r
	}
	sum := sha256.Sum256([]byte(real))
	return hex.EncodeToString(sum[:])[:16]
}

// SiloconfHash returns hex(sha256(.siloconf)) for projectRoot, or ("", nil) if
// no .siloconf is present. Used as a fingerprint by smart adoption to match a
// moved project against its old state dir.
func SiloconfHash(projectRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(projectRoot, ".siloconf"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// LoadOrCreateMeta resolves the meta for projectRoot:
//
//  1. Compute id from explicit project_id (if any) else path hash.
//  2. If ~/.silo/projects/<id>/meta.json exists, load + return.
//  3. Otherwise try smart adoption: if exactly one orphaned meta has a
//     siloconfHash that matches the project's current .siloconf AND its
//     recorded path no longer exists on disk, rename that dir to <id> and
//     return the loaded meta (with .Path updated to projectRoot).
//  4. Otherwise return a fresh in-memory meta with the new id.
//
// The returned meta is not yet persisted; callers persist via Touch.
func LoadOrCreateMeta(explicitID, projectRoot string) (*ProjectMeta, string, error) {
	id := ProjectID(explicitID, projectRoot)
	if m, err := readMeta(ProjectMetaPath(id)); err != nil {
		return nil, "", err
	} else if m != nil {
		// Make sure the recorded path matches the current one — useful when a
		// project_id is set and the user moved the dir; we silently update.
		if m.Path != projectRoot {
			m.Path = projectRoot
		}
		return m, id, nil
	}

	sh, _ := SiloconfHash(projectRoot)
	if sh != "" {
		if m, err := tryAdopt(projectRoot, id, sh); err == nil && m != nil {
			m.Path = projectRoot
			m.SiloconfHash = sh
			if explicitID != "" {
				m.ProjectID = explicitID
			}
			return m, id, nil
		}
	}
	return &ProjectMeta{
		SchemaVersion: ProjectMetaSchemaVersion,
		Path:          projectRoot,
		ProjectID:     explicitID,
		SiloconfHash:  sh,
	}, id, nil
}

func readMeta(path string) (*ProjectMeta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m ProjectMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

func writeMeta(id string, m *ProjectMeta) error {
	if err := os.MkdirAll(ProjectStateDir(id), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	target := ProjectMetaPath(id)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Touch updates lastUsedAt + tools + tool_to_recipe and persists meta.json
// atomically. Tools are merged into the existing list (deduped, sorted);
// tool_to_recipe entries overwrite on conflict. SiloconfHash is refreshed if
// the .siloconf at m.Path has changed since the meta was last written.
func Touch(id string, m *ProjectMeta, tools []string, toolToRecipe map[string]string) error {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = ProjectMetaSchemaVersion
	}
	m.LastUsedAt = time.Now().UTC()
	if sh, err := SiloconfHash(m.Path); err == nil && sh != "" {
		m.SiloconfHash = sh
	}
	if len(tools) > 0 {
		seen := map[string]struct{}{}
		for _, t := range m.Tools {
			seen[t] = struct{}{}
		}
		for _, t := range tools {
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			m.Tools = append(m.Tools, t)
		}
		sort.Strings(m.Tools)
	}
	if len(toolToRecipe) > 0 {
		if m.ToolToRecipe == nil {
			m.ToolToRecipe = map[string]string{}
		}
		for k, v := range toolToRecipe {
			m.ToolToRecipe[k] = v
		}
	}
	return writeMeta(id, m)
}

// tryAdopt scans ~/.silo/projects/*/meta.json for an entry whose
// siloconfHash matches sh AND whose recorded .Path is missing on disk. If
// exactly one matches, rename its dir to newID and return the loaded meta.
// Returns (nil, nil) on no/multiple matches. Logs a one-line notice on
// successful adoption.
func tryAdopt(projectRoot, newID, sh string) (*ProjectMeta, error) {
	entries, err := os.ReadDir(Projects())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var matchID string
	var matchMeta *ProjectMeta
	matches := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		oldID := e.Name()
		if oldID == newID {
			continue
		}
		m, err := readMeta(ProjectMetaPath(oldID))
		if err != nil || m == nil || m.SiloconfHash != sh {
			continue
		}
		if _, err := os.Stat(m.Path); err == nil {
			continue
		}
		matches++
		matchID = oldID
		matchMeta = m
	}
	if matches != 1 {
		return nil, nil
	}
	if err := os.Rename(ProjectStateDir(matchID), ProjectStateDir(newID)); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "silo: project appears to have moved from %s to %s; re-keyed state\n",
		matchMeta.Path, projectRoot)
	return matchMeta, nil
}

// ProjectListing pairs a project's id (~/.silo/projects/<id>) with its parsed
// meta.json. ListProjects skips entries with unparseable metas — the cleanup
// path treats them as orphans separately if needed.
type ProjectListing struct {
	ID   string
	Meta *ProjectMeta
}

// ListProjects walks ~/.silo/projects and returns one entry per directory
// that contains a parseable meta.json. Sorted by id for stable output.
// Missing ~/.silo/projects returns an empty slice, no error.
func ListProjects() ([]ProjectListing, error) {
	entries, err := os.ReadDir(Projects())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ProjectListing
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := readMeta(ProjectMetaPath(e.Name()))
		if err != nil || m == nil {
			continue
		}
		out = append(out, ProjectListing{ID: e.Name(), Meta: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListBakedHashes returns the set of recipe hashes currently present under
// ~/.silo/baked. Missing ~/.silo/baked returns an empty slice, no error.
func ListBakedHashes() ([]string, error) {
	entries, err := os.ReadDir(Baked())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// ResolveProjectRootfs returns the path the engine should use as tier-1 in
// its rootfs cascade for (projectRoot, tool), or "" if neither lookup hits.
//
// Order:
//  1. Auto-bake from .siloconf — looked up via the project meta's
//     tool_to_recipe[tool] -> ~/.silo/baked/<hash>/rootfs.ext4.
//  2. User-driven `silo build` output at <projectRoot>/.silo/<tool>/rootfs.ext4.
//
// The returned path is guaranteed to exist (Stat succeeds) at lookup time.
// explicitProjectID is the .siloconf `project_id:` field, or "".
func ResolveProjectRootfs(projectRoot, tool, explicitProjectID string) string {
	if projectRoot == "" {
		return ""
	}
	id := ProjectID(explicitProjectID, projectRoot)
	if m, err := readMeta(ProjectMetaPath(id)); err == nil && m != nil {
		if hash, ok := m.ToolToRecipe[tool]; ok && hash != "" {
			p := BakedRootfs(hash)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	p := ProjectRootfs(projectRoot, tool)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// MigrateLegacyProjectDir clears pre-0.5.0 auto-bake artifacts under
// <projectRoot>/.silo/<tool>/. We only touch entries that carry the
// `.sha256` sidecar that the old project_bake.go always wrote alongside its
// rootfs.ext4 — anything without that sidecar is treated as a user-driven
// `silo build` output and left alone. (Relocating those is a separate
// follow-up; see plan file.)
//
// Removed entries will be re-baked into ~/.silo/baked/ on the next sync,
// which is cheap because the OCI base is still cached. Per-entry failures
// log a warning and skip; the outer .silo/ directory is removed only if it
// ends up empty.
func MigrateLegacyProjectDir(projectRoot string) error {
	legacy := filepath.Join(projectRoot, ".silo")
	info, err := os.Stat(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(legacy)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(legacy, e.Name())
		rootfs := filepath.Join(sub, "rootfs.ext4")
		hashFile := rootfs + ".sha256"
		if _, err := os.Stat(rootfs); err != nil {
			continue
		}
		if _, err := os.Stat(hashFile); err != nil {
			// No sidecar -> almost certainly a `silo build` output. Skip.
			continue
		}
		if err := os.RemoveAll(sub); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove legacy %s: %v\n", sub, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "silo: removed legacy %s (will rebake into ~/.silo/baked/ on next sync)\n", sub)
	}
	leftovers, err := os.ReadDir(legacy)
	if err == nil && len(leftovers) == 0 {
		_ = os.Remove(legacy)
	}
	return nil
}
