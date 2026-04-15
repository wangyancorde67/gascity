package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type routeExpectation struct {
	name       string
	method     string
	path       string
	wantStatus int
}

func assertRouteStatuses(t *testing.T, h http.Handler, routes []routeExpectation) {
	t.Helper()

	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestCityLegacyHTTPRoutesUnavailable(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	// Inventory derived from the merge-base HTTP/SSE city surface. Non-survivor
	// operations should stay unavailable after the WS cutover.
	routes := []routeExpectation{
		{name: "status", method: http.MethodGet, path: "/v0/status", wantStatus: http.StatusNotFound},
		{name: "city get", method: http.MethodGet, path: "/v0/city", wantStatus: http.StatusMethodNotAllowed},
		{name: "city patch", method: http.MethodPatch, path: "/v0/city", wantStatus: http.StatusForbidden},
		{name: "agents list", method: http.MethodGet, path: "/v0/agents", wantStatus: http.StatusNotFound},
		{name: "agent get", method: http.MethodGet, path: "/v0/agent/coder", wantStatus: http.StatusNotFound},
		{name: "agent output", method: http.MethodGet, path: "/v0/agent/coder/output", wantStatus: http.StatusNotFound},
		{name: "agent output stream", method: http.MethodGet, path: "/v0/agent/coder/output/stream", wantStatus: http.StatusNotFound},
		{name: "agent create", method: http.MethodPost, path: "/v0/agents", wantStatus: http.StatusForbidden},
		{name: "agent update", method: http.MethodPatch, path: "/v0/agent/coder", wantStatus: http.StatusForbidden},
		{name: "agent delete", method: http.MethodDelete, path: "/v0/agent/coder", wantStatus: http.StatusForbidden},
		{name: "agent action", method: http.MethodPost, path: "/v0/agent/coder/suspend", wantStatus: http.StatusForbidden},
		{name: "config get", method: http.MethodGet, path: "/v0/config", wantStatus: http.StatusNotFound},
		{name: "config explain", method: http.MethodGet, path: "/v0/config/explain", wantStatus: http.StatusNotFound},
		{name: "config validate", method: http.MethodGet, path: "/v0/config/validate", wantStatus: http.StatusNotFound},
		{name: "agent patches list", method: http.MethodGet, path: "/v0/patches/agents", wantStatus: http.StatusNotFound},
		{name: "agent patch get", method: http.MethodGet, path: "/v0/patches/agent/coder", wantStatus: http.StatusNotFound},
		{name: "agent patch set", method: http.MethodPut, path: "/v0/patches/agents", wantStatus: http.StatusForbidden},
		{name: "agent patch delete", method: http.MethodDelete, path: "/v0/patches/agent/coder", wantStatus: http.StatusForbidden},
		{name: "rig patches list", method: http.MethodGet, path: "/v0/patches/rigs", wantStatus: http.StatusNotFound},
		{name: "provider patches list", method: http.MethodGet, path: "/v0/patches/providers", wantStatus: http.StatusNotFound},
		{name: "providers list", method: http.MethodGet, path: "/v0/providers", wantStatus: http.StatusNotFound},
		{name: "provider get", method: http.MethodGet, path: "/v0/provider/claude", wantStatus: http.StatusNotFound},
		{name: "provider create", method: http.MethodPost, path: "/v0/providers", wantStatus: http.StatusForbidden},
		{name: "rigs list", method: http.MethodGet, path: "/v0/rigs", wantStatus: http.StatusNotFound},
		{name: "rig get", method: http.MethodGet, path: "/v0/rig/default", wantStatus: http.StatusNotFound},
		{name: "rig create", method: http.MethodPost, path: "/v0/rigs", wantStatus: http.StatusForbidden},
		{name: "rig action", method: http.MethodPost, path: "/v0/rig/default/suspend", wantStatus: http.StatusForbidden},
		{name: "beads list", method: http.MethodGet, path: "/v0/beads", wantStatus: http.StatusNotFound},
		{name: "beads ready", method: http.MethodGet, path: "/v0/beads/ready", wantStatus: http.StatusNotFound},
		{name: "bead get", method: http.MethodGet, path: "/v0/bead/gc-1", wantStatus: http.StatusNotFound},
		{name: "bead close", method: http.MethodPost, path: "/v0/bead/gc-1/close", wantStatus: http.StatusForbidden},
		{name: "mail list", method: http.MethodGet, path: "/v0/mail", wantStatus: http.StatusNotFound},
		{name: "mail send", method: http.MethodPost, path: "/v0/mail", wantStatus: http.StatusForbidden},
		{name: "mail count", method: http.MethodGet, path: "/v0/mail/count", wantStatus: http.StatusNotFound},
		{name: "mail thread", method: http.MethodGet, path: "/v0/mail/thread/thread-1", wantStatus: http.StatusNotFound},
		{name: "convoys list", method: http.MethodGet, path: "/v0/convoys", wantStatus: http.StatusNotFound},
		{name: "convoy get", method: http.MethodGet, path: "/v0/convoy/cv-1", wantStatus: http.StatusNotFound},
		{name: "convoy create", method: http.MethodPost, path: "/v0/convoys", wantStatus: http.StatusForbidden},
		{name: "events list", method: http.MethodGet, path: "/v0/events", wantStatus: http.StatusNotFound},
		{name: "events stream", method: http.MethodGet, path: "/v0/events/stream", wantStatus: http.StatusNotFound},
		{name: "events emit", method: http.MethodPost, path: "/v0/events", wantStatus: http.StatusForbidden},
		{name: "orders list", method: http.MethodGet, path: "/v0/orders", wantStatus: http.StatusNotFound},
		{name: "orders feed", method: http.MethodGet, path: "/v0/orders/feed", wantStatus: http.StatusNotFound},
		{name: "order get", method: http.MethodGet, path: "/v0/order/daily", wantStatus: http.StatusNotFound},
		{name: "formulas list", method: http.MethodGet, path: "/v0/formulas", wantStatus: http.StatusNotFound},
		{name: "formula detail", method: http.MethodGet, path: "/v0/formula/daily", wantStatus: http.StatusNotFound},
		{name: "workflow get", method: http.MethodGet, path: "/v0/workflow/wf-1", wantStatus: http.StatusNotFound},
		{name: "sessions list", method: http.MethodGet, path: "/v0/sessions", wantStatus: http.StatusNotFound},
		{name: "session create", method: http.MethodPost, path: "/v0/sessions", wantStatus: http.StatusForbidden},
		{name: "session get", method: http.MethodGet, path: "/v0/session/s-1", wantStatus: http.StatusNotFound},
		{name: "session transcript", method: http.MethodGet, path: "/v0/session/s-1/transcript", wantStatus: http.StatusNotFound},
		{name: "session pending", method: http.MethodGet, path: "/v0/session/s-1/pending", wantStatus: http.StatusNotFound},
		{name: "session stream", method: http.MethodGet, path: "/v0/session/s-1/stream", wantStatus: http.StatusNotFound},
		{name: "session submit", method: http.MethodPost, path: "/v0/session/s-1/submit", wantStatus: http.StatusForbidden},
		{name: "session respond", method: http.MethodPost, path: "/v0/session/s-1/respond", wantStatus: http.StatusForbidden},
		{name: "packs list", method: http.MethodGet, path: "/v0/packs", wantStatus: http.StatusNotFound},
		{name: "sling", method: http.MethodPost, path: "/v0/sling", wantStatus: http.StatusForbidden},
		{name: "services list", method: http.MethodGet, path: "/v0/services", wantStatus: http.StatusNotFound},
		{name: "service get", method: http.MethodGet, path: "/v0/service/review-intake", wantStatus: http.StatusNotFound},
		{name: "service restart", method: http.MethodPost, path: "/v0/service/review-intake/restart", wantStatus: http.StatusForbidden},
	}

	assertRouteStatuses(t, srv, routes)
}

func TestSupervisorLegacyHTTPRoutesUnavailable(t *testing.T) {
	s1 := newFakeState(t)
	s1.cityName = "alpha"
	sm := newTestSupervisorMux(t, map[string]*fakeState{"alpha": s1})

	routes := []routeExpectation{
		{name: "cities list", method: http.MethodGet, path: "/v0/cities", wantStatus: http.StatusNotFound},
		{name: "city get", method: http.MethodGet, path: "/v0/city", wantStatus: http.StatusNotFound},
		{name: "city patch", method: http.MethodPatch, path: "/v0/city", wantStatus: http.StatusNotFound},
		{name: "global events list", method: http.MethodGet, path: "/v0/events", wantStatus: http.StatusNotFound},
		{name: "global events stream", method: http.MethodGet, path: "/v0/events/stream", wantStatus: http.StatusNotFound},
		{name: "bare status compat", method: http.MethodGet, path: "/v0/status", wantStatus: http.StatusNotFound},
		{name: "bare agents compat", method: http.MethodGet, path: "/v0/agents", wantStatus: http.StatusNotFound},
		{name: "bare session stream compat", method: http.MethodGet, path: "/v0/session/s-1/stream", wantStatus: http.StatusNotFound},
		{name: "namespaced city detail", method: http.MethodGet, path: "/v0/city/alpha", wantStatus: http.StatusBadRequest},
		{name: "namespaced agents", method: http.MethodGet, path: "/v0/city/alpha/agents", wantStatus: http.StatusNotFound},
		{name: "namespaced agent output", method: http.MethodGet, path: "/v0/city/alpha/agent/coder/output", wantStatus: http.StatusNotFound},
		{name: "namespaced session get", method: http.MethodGet, path: "/v0/city/alpha/session/s-1", wantStatus: http.StatusNotFound},
	}

	assertRouteStatuses(t, sm, routes)
}
