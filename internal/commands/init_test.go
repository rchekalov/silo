// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendToGitignore_NoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := appendToGitignore(path, ".silo/"); err == nil {
		t.Fatal("expected error when .gitignore does not exist, got nil")
	}
}

func TestAppendToGitignore_AddsEntryWithNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("node_modules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendToGitignore(path, ".silo/"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "node_modules\n.silo/\n" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestAppendToGitignore_SkipsDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	original := "node_modules\n.silo/\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendToGitignore(path, ".silo/"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestAppendToGitignore_PreservesTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendToGitignore(path, "bar"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(got), "\nbar\n") {
		t.Fatalf("unexpected content: %q", got)
	}
}
