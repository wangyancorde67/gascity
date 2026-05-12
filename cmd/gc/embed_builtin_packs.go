package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gastownhall/gascity/examples/bd"
	"github.com/gastownhall/gascity/examples/dolt"
	"github.com/gastownhall/gascity/examples/gastown/packs/gastown"
	"github.com/gastownhall/gascity/examples/gastown/packs/maintenance"
	"github.com/gastownhall/gascity/internal/bootstrap/packs/core"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/orders"
)

// builtinPack pairs an embedded FS with the subdirectory name used under .gc/system/packs/.
type builtinPack struct {
	fs   fs.FS
	name string // e.g. "bd", "dolt"
}

const (
	legacyOrderConfigFile = "order.toml"
)

// builtinPacks lists all packs embedded in the gc binary. New city config
// installs these through the pack registry; .gc/system/packs remains a
// compatibility/materialized-assets location for legacy configs and managed
// provider scripts.
var builtinPacks = []builtinPack{
	{fs: core.PackFS, name: "core"},
	{fs: bd.PackFS, name: "bd"},
	{fs: dolt.PackFS, name: "dolt"},
	{fs: maintenance.PackFS, name: "maintenance"},
	{fs: gastown.PackFS, name: "gastown"},
}

// MaterializeBuiltinPacks writes all embedded pack files to
// .gc/system/packs/{name}/ in the city directory. Files whose content and mode
// already match are left in place; changed content or mode is repaired with an
// atomic rename so readers never observe a truncated file. Shell scripts get
// 0755; everything else 0644.
// Idempotent: safe to call on every gc start and gc init.
func MaterializeBuiltinPacks(cityPath string) error {
	for _, bp := range builtinPacks {
		dst := filepath.Join(cityPath, citylayout.SystemPacksRoot, bp.name)
		desired, err := materializeFS(bp.fs, ".", dst)
		if err != nil {
			return fmt.Errorf("materializing %s pack: %w", bp.name, err)
		}
		if err := pruneStaleGeneratedPackFiles(dst, desired); err != nil {
			return fmt.Errorf("pruning stale %s pack files: %w", bp.name, err)
		}
		if err := pruneLegacyEmbeddedOrders(bp.fs, dst); err != nil {
			return fmt.Errorf("pruning legacy %s order paths: %w", bp.name, err)
		}
	}
	return nil
}

func usesOSFS(fs fsys.FS) bool {
	switch fs.(type) {
	case fsys.OSFS, *fsys.OSFS:
		return true
	default:
		return false
	}
}

func requiredBuiltinPackNames(cityPath string) []string {
	required := []string{"core", "maintenance"}

	provider := strings.TrimSpace(configuredBeadsProviderValue(cityPath))
	normalizedProvider := normalizeRawBeadsProvider(cityPath, provider)
	if providerUsesBdStoreContract(normalizedProvider) {
		required = append(required, "bd")
	}
	usesDirectExecLifecycle := strings.HasPrefix(provider, "exec:") &&
		execProviderBase(provider) == "gc-beads-bd" &&
		normalizedProvider != "bd"
	if usesDirectExecLifecycle {
		required = append(required, "dolt")
	}
	return required
}

// builtinPackIncludes returns the legacy compatibility include set for
// materialized .gc/system/packs content. Normal config loading no longer
// calls this helper; new and migrated cities use explicit bundled imports
// resolved through packs.lock and the pack registry cache.
func builtinPackIncludes(cityPath string) []string {
	systemRoot := filepath.Join(cityPath, citylayout.SystemPacksRoot)

	var includes []string
	for _, name := range requiredBuiltinPackNames(cityPath) {
		packPath := filepath.Join(systemRoot, name)
		if packExists(packPath) {
			includes = append(includes, packPath)
		}
	}

	return includes
}

// packExists checks if a pack.toml exists in the given directory.
func packExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "pack.toml"))
	return err == nil
}

// peekBeadsProvider reads just the beads.provider field from a city.toml
// without doing full config parsing. Returns "" if not set or on error.
func peekBeadsProvider(tomlPath string) string {
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		return ""
	}
	var peek struct {
		Beads struct {
			Provider string `toml:"provider"`
		} `toml:"beads"`
	}
	if _, err := toml.Decode(string(data), &peek); err != nil {
		return ""
	}
	return peek.Beads.Provider
}

// materializeFS walks an embed.FS rooted at root, writes all files to dstDir,
// and returns the relative file paths that belong in the generated directory.
func materializeFS(embedded fs.FS, root, dstDir string) (map[string]struct{}, error) {
	desired := make(map[string]struct{})
	err := fs.WalkDir(embedded, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute the relative path from root.
		rel := path
		if root != "." {
			rel = strings.TrimPrefix(path, root+"/")
			if rel == root {
				return nil
			}
		}

		dst := filepath.Join(dstDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		desired[filepath.ToSlash(rel)] = struct{}{}

		data, err := fs.ReadFile(embedded, path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}

		perm := builtinPackFileMode(path)
		return fsys.WriteFileIfContentOrModeChangedAtomic(fsys.OSFS{}, dst, data, perm)
	})
	if err != nil {
		return nil, err
	}
	return desired, nil
}

// isExecutableScriptFilename reports whether a materialized pack asset
// should be marked executable. Shell, Python, and bash interpreters all
// rely on shebang-based direct execution, so the file needs +x regardless
// of extension — gc invokes resolved run paths directly rather than
// wrapping them with an explicit interpreter command.
func isExecutableScriptFilename(name string) bool {
	for _, suffix := range []string{".sh", ".py", ".bash"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func builtinPackFileMode(name string) os.FileMode {
	if isExecutableScriptFilename(name) {
		return 0o755
	}
	return 0o644
}

// pruneLegacyEmbeddedOrders removes deprecated order directory layouts when the
// embedded pack already provides the flat orders/<name>.toml form.
func pruneLegacyEmbeddedOrders(embedded fs.FS, dstDir string) error {
	entries, err := fs.ReadDir(embedded, "orders")
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		orderName, ok := orders.TrimFlatOrderFilename(name)
		if !ok {
			continue
		}
		for _, legacyPath := range []string{
			filepath.Join(dstDir, "orders", orderName, legacyOrderConfigFile),
			filepath.Join(dstDir, "formulas", "orders", orderName, legacyOrderConfigFile),
		} {
			if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			pruneEmptyDirs(filepath.Dir(legacyPath), dstDir)
		}
	}
	return nil
}

// pruneStaleGeneratedPackFiles treats the current binary's embedded pack tree
// as the source of truth for generated files. Concurrent older/newer binaries
// can briefly prune each other's obsolete generated-only files, but the next
// successful materialization self-heals the directory to the active binary.
func pruneStaleGeneratedPackFiles(dstDir string, desired map[string]struct{}) error {
	if _, err := os.Stat(dstDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	dirsToPrune := make(map[string]struct{})
	if err := filepath.WalkDir(dstDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dstDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := desired[rel]; ok {
			return nil
		}
		// Ignore in-flight atomic temp files so concurrent refreshes do not
		// delete each other's rename targets mid-write.
		if isGeneratedPackAtomicTempRel(rel, func(path string) bool {
			_, ok := desired[path]
			return ok
		}) {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		dirsToPrune[filepath.Dir(path)] = struct{}{}
		return nil
	}); err != nil {
		return err
	}

	pruneDirs := make([]string, 0, len(dirsToPrune))
	for dir := range dirsToPrune {
		pruneDirs = append(pruneDirs, dir)
	}
	sort.Slice(pruneDirs, func(i, j int) bool {
		left := filepath.Clean(pruneDirs[i])
		right := filepath.Clean(pruneDirs[j])
		leftDepth := strings.Count(left, string(filepath.Separator))
		rightDepth := strings.Count(right, string(filepath.Separator))
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return left > right
	})
	for _, dir := range pruneDirs {
		pruneEmptyDirs(dir, dstDir)
	}
	return nil
}

func isGeneratedPackAtomicTempRel(rel string, hasDesired func(string) bool) bool {
	idx := strings.LastIndex(rel, ".tmp.")
	return idx > 0 && hasDesired(rel[:idx])
}

func pruneEmptyDirs(dir, stop string) {
	stop = filepath.Clean(stop)
	for {
		cleanDir := filepath.Clean(dir)
		if cleanDir == stop || cleanDir == "." || cleanDir == string(filepath.Separator) {
			return
		}
		entries, err := os.ReadDir(cleanDir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(cleanDir); err != nil {
			return
		}
		dir = filepath.Dir(cleanDir)
	}
}
