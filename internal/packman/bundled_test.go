package packman

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/builtinpacks"
)

func TestEnsureRepoInCacheMaterializesBundledSourceWithoutGit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := builtinpacks.MustSource("maintenance")
	commit := "abc123"

	prev := runGit
	runGit = func(_ string, args ...string) (string, error) {
		return "", fmt.Errorf("unexpected git call for bundled pack: %v", args)
	}
	t.Cleanup(func() { runGit = prev })

	got, err := EnsureRepoInCache(source, commit)
	if err != nil {
		t.Fatalf("EnsureRepoInCache: %v", err)
	}
	want, err := RepoCachePath(source, commit)
	if err != nil {
		t.Fatalf("RepoCachePath: %v", err)
	}
	if got != want {
		t.Fatalf("EnsureRepoInCache path = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(got, ".git")); !os.IsNotExist(err) {
		t.Fatalf("synthetic cache should not contain .git, stat err = %v", err)
	}
	packToml := filepath.Join(got, "examples", "gastown", "packs", "maintenance", "pack.toml")
	if _, err := os.Stat(packToml); err != nil {
		t.Fatalf("synthetic cache missing maintenance pack.toml: %v", err)
	}
	if err := builtinpacks.ValidateSyntheticRepo(got, commit); err != nil {
		t.Fatalf("ValidateSyntheticRepo: %v", err)
	}
}
