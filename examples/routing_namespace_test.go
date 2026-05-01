package examples_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShippedExamplesDoNotHardcodeShortRoutedToPools(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)
	badRoutes := []string{
		"gc.routed_to=dog",
		"gc.routed_to=worker",
		"gc.routed_to=<rig>/polecat",
		"gc.routed_to=<rig>/refinery",
		"gc.routed_to={{ .RigName }}/refinery",
	}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(data)
		for _, bad := range badRoutes {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains short-form routed_to target %q", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExamplePoolScriptsUseCanonicalGCTemplateRoutes(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)

	checks := []struct {
		rel      string
		required []string
		banned   []string
	}{
		{
			rel: "hyperscale/packs/hyperscale/assets/scripts/mock-worker.sh",
			required: []string{
				`POOL_LABEL="${GC_TEMPLATE:?`,
				`gc.routed_to=$POOL_LABEL`,
			},
			banned: []string{
				`POOL_LABEL="${GC_TEMPLATE:-worker}"`,
			},
		},
		{
			rel: "lifecycle/packs/lifecycle/assets/scripts/mock-polecat.sh",
			required: []string{
				`POOL_LABEL="${GC_TEMPLATE:?`,
				`REFINERY="${GC_TEMPLATE%polecat}refinery"`,
				`gc.routed_to=$POOL_LABEL`,
			},
			banned: []string{
				`POOL_LABEL="$GC_AGENT"`,
				`REFINERY="${GC_AGENT%/*}/refinery"`,
			},
		},
	}

	for _, check := range checks {
		path := filepath.Join(root, check.rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", check.rel, err)
		}
		body := string(data)
		for _, required := range check.required {
			if !strings.Contains(body, required) {
				t.Errorf("%s missing canonical route pattern %q", check.rel, required)
			}
		}
		for _, banned := range check.banned {
			if strings.Contains(body, banned) {
				t.Errorf("%s still contains short-form route pattern %q", check.rel, banned)
			}
		}
	}
}
