package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/builtinpacks"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestLoadWithIncludesAcceptsSyntheticBundledImportCache(t *testing.T) {
	home := t.TempDir()
	city := t.TempDir()
	t.Setenv("HOME", home)

	source := builtinpacks.MustSource("core")
	commit := "abc123"
	cacheDir := filepath.Join(home, ".gc", "cache", "repos", RepoCacheKey(source, commit))
	if err := builtinpacks.MaterializeSyntheticRepo(cacheDir, commit); err != nil {
		t.Fatalf("MaterializeSyntheticRepo: %v", err)
	}
	writeBundledImportTestFile(t, filepath.Join(city, "city.toml"), "[workspace]\n")
	writeBundledImportTestFile(t, filepath.Join(city, "pack.toml"), `
[pack]
name = "city"
schema = 2

[imports.core]
source = "`+source+`"
`)
	writeBundledImportTestFile(t, filepath.Join(city, "packs.lock"), `
schema = 1

[packs."`+source+`"]
version = "sha:`+commit+`"
commit = "`+commit+`"
fetched = "`+time.Unix(1, 0).UTC().Format(time.RFC3339)+`"
`)

	cfg, _, err := LoadWithIncludes(fsys.OSFS{}, filepath.Join(city, "city.toml"))
	if err != nil {
		t.Fatalf("LoadWithIncludes: %v", err)
	}
	if len(cfg.ExplicitImportPackDirs) == 0 {
		t.Fatal("ExplicitImportPackDirs is empty")
	}
	if got := filepath.ToSlash(cfg.ExplicitImportPackDirs[0]); !strings.HasSuffix(got, "internal/bootstrap/packs/core") {
		t.Fatalf("ExplicitImportPackDirs[0] = %q, want core cache subpath", cfg.ExplicitImportPackDirs[0])
	}
}

func writeBundledImportTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
