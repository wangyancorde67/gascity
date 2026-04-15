package dashboard

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

//go:embed static
var staticFiles embed.FS

// NewDashboardMux creates an HTTP handler that serves the dashboard as fully
// static files plus a WebSocket reverse proxy at /v0/ws. The browser derives
// its WebSocket URL from window.location, so no HTML templating is needed.
//
// apiURL is the upstream supervisor/city API (e.g. "http://localhost:7860").
// initialCityScope is kept for backwards compatibility with callers but is no
// longer injected into HTML; the browser defaults its city scope via cities.list.
func NewDashboardMux(apiURL, _ string) (http.Handler, error) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	upstream, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("dashboard: invalid api URL %q: %w", apiURL, err)
	}
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return nil, fmt.Errorf("dashboard: api URL must be http(s): %q", apiURL)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	// Force IPv4 when the upstream hostname is "localhost" — Go's default
	// dialer prefers IPv6 ([::1]) but the supervisor typically listens on
	// IPv4 only (127.0.0.1).
	if upstream.Hostname() == "localhost" {
		proxy.Transport = &http.Transport{
			DialContext: (&net.Dialer{}).DialContext,
			ForceAttemptHTTP2: true,
		}
		// Rewrite the upstream URL to 127.0.0.1 so the dialer doesn't
		// resolve "localhost" to [::1].
		upstream.Host = strings.Replace(upstream.Host, "localhost", "127.0.0.1", 1)
		proxy = httputil.NewSingleHostReverseProxy(upstream)
	}
	// Preserve the upstream host header so the supervisor accepts the
	// WebSocket upgrade (gorilla/websocket validates Origin/Host).
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = upstream.Host
	}

	mux := http.NewServeMux()
	mux.Handle("/v0/", proxy) // forwards WS + any future API paths to upstream
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve index.html for any non-static, non-API path. No templating.
		if strings.HasPrefix(r.URL.Path, "/static/") || strings.HasPrefix(r.URL.Path, "/v0/") {
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
