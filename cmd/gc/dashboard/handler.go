package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

//go:embed static
var staticFiles embed.FS

type bootstrapConfig struct {
	APIBaseURL       string `json:"apiBaseURL"`
	InitialCityScope string `json:"initialCityScope,omitempty"`
}

// NewDashboardMux creates an HTTP handler that serves the dashboard as static
// assets plus a tiny bootstrap script. The browser uses the bootstrap config
// to connect directly to the supervisor WebSocket endpoint; the dashboard
// server never proxies API or WebSocket traffic.
func NewDashboardMux(apiURL, initialCityScope string) (http.Handler, error) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	parsedAPIURL, err := url.Parse(strings.TrimRight(apiURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("dashboard: invalid api URL %q: %w", apiURL, err)
	}
	if parsedAPIURL.Scheme != "http" && parsedAPIURL.Scheme != "https" {
		return nil, fmt.Errorf("dashboard: api URL must be http(s): %q", apiURL)
	}
	parsedAPIURL.Path = strings.TrimRight(parsedAPIURL.Path, "/")
	parsedAPIURL.RawQuery = ""
	parsedAPIURL.Fragment = ""
	bootstrap := bootstrapConfig{
		APIBaseURL:       parsedAPIURL.String(),
		InitialCityScope: initialCityScope,
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/bootstrap.js", func(w http.ResponseWriter, _ *http.Request) {
		body, err := renderBootstrapScript(bootstrap)
		if err != nil {
			http.Error(w, "dashboard bootstrap failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/v0/") ||
			strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			http.Error(w, "dashboard not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	return mux, nil
}

func renderBootstrapScript(cfg bootstrapConfig) ([]byte, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return append([]byte("window.__GC_BOOTSTRAP__ = "), append(data, []byte(";\n")...)...), nil
}
