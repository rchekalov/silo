// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectIDPrefersExplicitID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := ProjectID("01HZZULID", "/tmp/whatever")
	if got != "01HZZULID" {
		t.Fatalf("explicit id should win: got %q", got)
	}
}

func TestProjectIDPathHashIsStableAndSymlinkResolved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	a := ProjectID("", dir)
	b := ProjectID("", link)
	if a == "" {
		t.Fatal("path-hash id should be non-empty")
	}
	if a != b {
		t.Fatalf("symlinked path should hash to same id: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("path-hash id should be 16 hex chars, got %d", len(a))
	}
}

func TestLoadOrCreateMetaFreshThenReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".siloconf"), []byte("tools: [node]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m1, id1, err := LoadOrCreateMeta("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if m1.Path != dir {
		t.Fatalf("path=%q want %q", m1.Path, dir)
	}
	if m1.SiloconfHash == "" {
		t.Fatal("siloconfHash should be populated when .siloconf exists")
	}

	if err := Touch(id1, m1, []string{"node"}, map[string]string{"node": "abc123"}); err != nil {
		t.Fatal(err)
	}

	m2, id2, err := LoadOrCreateMeta("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("id changed across loads: %q vs %q", id1, id2)
	}
	if m2.ToolToRecipe["node"] != "abc123" {
		t.Fatalf("recipe not persisted: %v", m2.ToolToRecipe)
	}
	if len(m2.Tools) != 1 || m2.Tools[0] != "node" {
		t.Fatalf("tools not persisted: %v", m2.Tools)
	}
}

func TestLoadOrCreateMetaSmartAdoption(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldPath := t.TempDir()
	siloconf := []byte("tools: [node]\nproject: example\n")
	if err := os.WriteFile(filepath.Join(oldPath, ".siloconf"), siloconf, 0o644); err != nil {
		t.Fatal(err)
	}

	m, id, err := LoadOrCreateMeta("", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := Touch(id, m, []string{"node"}, map[string]string{"node": "abc"}); err != nil {
		t.Fatal(err)
	}

	// Simulate: user moves the project. The old path no longer exists; the
	// new path has the same .siloconf bytes.
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatal(err)
	}
	newPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(newPath, ".siloconf"), siloconf, 0o644); err != nil {
		t.Fatal(err)
	}

	m2, id2, err := LoadOrCreateMeta("", newPath)
	if err != nil {
		t.Fatal(err)
	}
	if id == id2 {
		t.Fatal("expected a new id at the new path")
	}
	if m2.Path != newPath {
		t.Fatalf("adopted meta path=%q want %q", m2.Path, newPath)
	}
	if m2.ToolToRecipe["node"] != "abc" {
		t.Fatal("adoption should preserve tool_to_recipe")
	}
	if _, err := os.Stat(ProjectStateDir(id)); !os.IsNotExist(err) {
		t.Fatal("old state dir should be gone after adoption")
	}
}

func TestLoadOrCreateMetaNoAdoptionOnMultipleMatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	siloconf := []byte("tools: [node]\n")
	sum := sha256.Sum256(siloconf)
	sh := hex.EncodeToString(sum[:])

	// Plant two orphan metas directly so smart-adoption can't consume one
	// while we set up the other.
	for _, missing := range []string{"/tmp/silo-missing-a", "/tmp/silo-missing-b"} {
		id := ProjectID("", missing)
		m := &ProjectMeta{
			SchemaVersion: ProjectMetaSchemaVersion,
			Path:          missing,
			SiloconfHash:  sh,
			Tools:         []string{"node"},
		}
		if err := writeMeta(id, m); err != nil {
			t.Fatal(err)
		}
	}

	newPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(newPath, ".siloconf"), siloconf, 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, err := LoadOrCreateMeta("", newPath)
	if err != nil {
		t.Fatal(err)
	}
	// With multiple matches, adoption is ambiguous -> fresh meta.
	if !m.LastUsedAt.IsZero() {
		t.Fatal("fresh meta should have zero lastUsedAt before Touch")
	}
	if m.Path != newPath {
		t.Fatalf("fresh meta path=%q want %q", m.Path, newPath)
	}
}

func TestMigrateLegacyProjectDirOnlyTouchesSidecarBakes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Legacy bake (with sidecar) — should be removed.
	bakeDir := filepath.Join(dir, ".silo", "node")
	if err := os.MkdirAll(bakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bakeDir, "rootfs.ext4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bakeDir, "rootfs.ext4.sha256"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	// User-driven `silo build` output (no sidecar) — should be preserved.
	buildDir := filepath.Join(dir, ".silo", "python")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "rootfs.ext4"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyProjectDir(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(bakeDir); !os.IsNotExist(err) {
		t.Fatal("legacy bake should be removed")
	}
	if _, err := os.Stat(filepath.Join(buildDir, "rootfs.ext4")); err != nil {
		t.Fatalf("user-driven build output should be preserved: %v", err)
	}

	// Idempotency.
	if err := MigrateLegacyProjectDir(dir); err != nil {
		t.Fatalf("second run should be a no-op: %v", err)
	}
}

func TestResolveProjectRootfsPrefersBakedOverBuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Project-build rootfs (legacy/silo build path).
	buildPath := ProjectRootfs(dir, "node")
	if err := os.MkdirAll(filepath.Dir(buildPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(buildPath, []byte("build"), 0o644); err != nil {
		t.Fatal(err)
	}

	// With no meta.json, resolver should fall through to the build path.
	got := ResolveProjectRootfs(dir, "node", "")
	if got != buildPath {
		t.Fatalf("with only build present, want %q got %q", buildPath, got)
	}

	// Now add a baked rootfs and a meta.json that points to it.
	hash := "deadbeefcafef00d"
	bakePath := BakedRootfs(hash)
	if err := os.MkdirAll(filepath.Dir(bakePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bakePath, []byte("baked"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := ProjectID("", dir)
	if err := os.MkdirAll(ProjectStateDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := ProjectMeta{
		SchemaVersion: ProjectMetaSchemaVersion,
		Path:          dir,
		Tools:         []string{"node"},
		ToolToRecipe:  map[string]string{"node": hash},
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectMetaPath(id), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	got = ResolveProjectRootfs(dir, "node", "")
	if got != bakePath {
		t.Fatalf("baked should win, want %q got %q", bakePath, got)
	}
}

func TestListProjectsAndBakedHashes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// No state dir yet -> no error, empty.
	if got, err := ListProjects(); err != nil || len(got) != 0 {
		t.Fatalf("empty ListProjects: %v %v", got, err)
	}
	if got, err := ListBakedHashes(); err != nil || len(got) != 0 {
		t.Fatalf("empty ListBakedHashes: %v %v", got, err)
	}

	a := t.TempDir()
	b := t.TempDir()
	for _, d := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(d, ".siloconf"), []byte("tools: [node]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m, id, err := LoadOrCreateMeta("", d)
		if err != nil {
			t.Fatal(err)
		}
		if err := Touch(id, m, []string{"node"}, map[string]string{"node": "h-" + filepath.Base(d)}); err != nil {
			t.Fatal(err)
		}
		// fake the baked rootfs dirs
		if err := os.MkdirAll(BakedDir("h-"+filepath.Base(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ListProjects()
	if err != nil || len(got) != 2 {
		t.Fatalf("expected 2 projects, got %d (err=%v)", len(got), err)
	}
	bakes, err := ListBakedHashes()
	if err != nil || len(bakes) != 2 {
		t.Fatalf("expected 2 bakes, got %v (err=%v)", bakes, err)
	}
}
