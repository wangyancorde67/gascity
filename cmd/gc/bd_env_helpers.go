package main

import (
	"sort"
	"strings"
)

var projectedDoltEnvKeys = []string{
	"GC_DOLT_HOST",
	"GC_DOLT_PORT",
	"GC_DOLT_USER",
	"GC_DOLT_PASSWORD",
	"BEADS_CREDENTIALS_FILE",
	"BEADS_DOLT_SERVER_HOST",
	"BEADS_DOLT_SERVER_PORT",
	"BEADS_DOLT_SERVER_USER",
	"BEADS_DOLT_PASSWORD",
}

func setProjectedDoltEnvEmpty(env map[string]string) {
	for _, key := range projectedDoltEnvKeys {
		env[key] = ""
	}
}

func ensureProjectedDoltEnvExplicit(env map[string]string) {
	for _, key := range projectedDoltEnvKeys {
		if _, ok := env[key]; !ok {
			env[key] = ""
		}
	}
}

func clearProjectedDoltEnv(env map[string]string) {
	for _, key := range projectedDoltEnvKeys {
		delete(env, key)
	}
}

func mirrorBeadsDoltEnv(env map[string]string) {
	if env == nil {
		return
	}
	if host := strings.TrimSpace(env["GC_DOLT_HOST"]); host != "" {
		env["BEADS_DOLT_SERVER_HOST"] = host
	} else {
		delete(env, "BEADS_DOLT_SERVER_HOST")
	}
	if port := strings.TrimSpace(env["GC_DOLT_PORT"]); port != "" {
		env["BEADS_DOLT_SERVER_PORT"] = port
	} else {
		// Keep the key present so child bd processes cannot inherit a stale
		// BEADS_DOLT_SERVER_PORT from an ambient parent environment.
		env["BEADS_DOLT_SERVER_PORT"] = ""
	}
	if user := strings.TrimSpace(env["GC_DOLT_USER"]); user != "" {
		env["BEADS_DOLT_SERVER_USER"] = user
	} else {
		delete(env, "BEADS_DOLT_SERVER_USER")
	}
	// Note: beads v1.0.0 reads BEADS_DOLT_PASSWORD (no _SERVER_ infix).
	// The asymmetry with BEADS_DOLT_SERVER_USER is intentional per beads
	// upstream convention.
	if pass := env["GC_DOLT_PASSWORD"]; pass != "" {
		env["BEADS_DOLT_PASSWORD"] = pass
	} else {
		delete(env, "BEADS_DOLT_PASSWORD")
	}
}

func overlayEnvEntries(environ []string, overrides map[string]string) []string {
	out := append([]string(nil), environ...)
	if len(overrides) == 0 {
		return out
	}
	overrideKeys := make([]string, 0, len(overrides))
	for key := range overrides {
		overrideKeys = append(overrideKeys, key)
	}
	sort.Strings(overrideKeys)
	for _, key := range overrideKeys {
		out = removeEnvKey(out, key)
		out = append(out, key+"="+overrides[key])
	}
	return out
}

func mergeRuntimeEnv(environ []string, overrides map[string]string) []string {
	keys := []string{
		"BEADS_CREDENTIALS_FILE",
		"BEADS_DIR",
		"BEADS_DOLT_AUTO_START",
		"BEADS_DOLT_PASSWORD",
		"BEADS_DOLT_SERVER_HOST",
		"BEADS_DOLT_SERVER_PORT",
		"BEADS_DOLT_SERVER_USER",
		"GC_CITY",
		"GC_CITY_ROOT", // kept for stripping: no code emits this anymore, but inherited values must be cleaned
		"GC_CITY_PATH",
		"GC_CITY_RUNTIME_DIR",
		"GC_DOLT",
		"GC_DOLT_HOST",
		"GC_DOLT_PASSWORD",
		"GC_DOLT_PORT",
		"GC_DOLT_USER",
		"GC_PACK_STATE_DIR",
		"GC_RIG",
		"GC_RIG_ROOT",
	}
	if len(overrides) > 0 {
		for key := range overrides {
			if !containsString(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	out := append([]string(nil), environ...)
	for _, key := range keys {
		out = removeEnvKey(out, key)
	}
	overrideKeys := make([]string, 0, len(overrides))
	for key := range overrides {
		overrideKeys = append(overrideKeys, key)
	}
	sort.Strings(overrideKeys)
	for _, key := range overrideKeys {
		out = append(out, key+"="+overrides[key])
	}
	return out
}

func removeEnvKey(environ []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
