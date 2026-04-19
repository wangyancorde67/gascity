package main

import (
	"path/filepath"

	"github.com/gastownhall/gascity/internal/config"
)

func loadedCityName(cfg *config.City, cityPath string) string {
	fallback := ""
	if cityPath != "" {
		fallback = filepath.Base(filepath.Clean(cityPath))
	}
	return config.EffectiveCityName(cfg, fallback)
}
