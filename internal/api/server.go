package api

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/config"
)

// Server is the GC API HTTP server. It serves /v0/* endpoints and /health.
type Server struct {
	state    State
	mux      *http.ServeMux
	server   *http.Server
	readOnly bool // when true, POST endpoints return 403

	// sessionLogSearchPaths overrides the default search paths for Claude
	// session JSONL files. Nil means use sessionlog.DefaultSearchPaths().
	sessionLogSearchPaths []string

	// idem caches responses for Idempotency-Key replay on create endpoints.
	idem *idempotencyCache

	// lookPathCache caches exec.LookPath results with a short TTL to avoid
	// repeated filesystem scans on every GET /v0/agents request.
	lookPathMu      sync.Mutex
	lookPathEntries map[string]lookPathEntry

	// responseCache memoizes expensive read responses for a short TTL so
	// repeated UI polls do not re-run the same bead-store subprocesses when
	// nothing material has changed.
	responseCacheMu      sync.Mutex
	responseCacheEntries map[string]responseCacheEntry

	// LookPathFunc can be overridden in tests. Defaults to exec.LookPath.
	LookPathFunc func(string) (string, error)

	// Domain services — extracted from the Server god object.
	// Each service has a focused interface and is independently testable.
	Beads     BeadService
	Mail      MailService
	Events    EventService
	Agents    AgentService
	Rigs      RigService
	Sessions  SessionService
	Convoys   ConvoyService
	Providers ProviderService
	Formulas  FormulaService
	Orders    OrderService
}

type lookPathEntry struct {
	found   bool
	expires time.Time
}

// cachedLookPath checks if a binary is in PATH, caching the result for lookPathCacheTTL.
func (s *Server) cachedLookPath(binary string) bool {
	s.lookPathMu.Lock()
	defer s.lookPathMu.Unlock()

	if s.lookPathEntries == nil {
		s.lookPathEntries = make(map[string]lookPathEntry)
	}

	if e, ok := s.lookPathEntries[binary]; ok && time.Now().Before(e.expires) {
		return e.found
	}

	lookPath := s.LookPathFunc
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath(binary)
	found := err == nil
	s.lookPathEntries[binary] = lookPathEntry{found: found, expires: time.Now().Add(lookPathCacheTTL)}
	return found
}

// resolveTitleProvider resolves the workspace default provider for title
// generation. Returns nil if the provider can't be resolved.
func (s *Server) resolveTitleProvider() *config.ResolvedProvider {
	cfg := s.state.Config()
	if cfg == nil {
		return nil
	}
	lookPath := s.LookPathFunc
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	rp, err := config.ResolveProvider(
		&config.Agent{},
		&cfg.Workspace,
		cfg.Providers,
		lookPath,
	)
	if err != nil {
		return nil
	}
	return rp
}

// New creates a Server with all routes registered. Does not start listening.
func New(state State) *Server {
	s := &Server{
		state: state,
		mux:   http.NewServeMux(),
		idem:  newIdempotencyCache(30 * time.Minute),
	}
	s.wireServices()
	s.registerRoutes()
	return s
}

// NewReadOnly creates a read-only Server that rejects all mutation requests.
// Use this when the server binds to a non-localhost address.
func NewReadOnly(state State) *Server {
	s := &Server{
		state:    state,
		mux:      http.NewServeMux(),
		readOnly: true,
		idem:     newIdempotencyCache(30 * time.Minute),
	}
	s.wireServices()
	s.registerRoutes()
	return s
}

// wireServices constructs all domain services. Called from New/NewReadOnly
// and test helpers that construct Server directly.
func (s *Server) wireServices() {
	s.Beads = &beadService{s: s}
	s.Mail = &mailService{s: s}
	s.Events = &eventService{s: s}
	s.Agents = &agentService{s: s}
	s.Rigs = &rigService{s: s}
	s.Sessions = &sessionService{s: s}
	s.Convoys = &convoyService{s: s}
	s.Providers = &providerService{s: s}
	s.Formulas = &formulaService{s: s}
	s.Orders = &orderService{s: s}
}

// ServeHTTP implements http.Handler for testing with httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler().ServeHTTP(w, r)
}

func (s *Server) handler() http.Handler {
	apiInner := withCSRFCheck(s.mux)
	if s.readOnly {
		apiInner = withReadOnly(apiInner)
	}
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/svc/") {
			// Workspace services apply their own publication and CSRF rules in
			// handleServiceProxy; they do not inherit controller API policy.
			s.mux.ServeHTTP(w, r)
			return
		}
		apiInner.ServeHTTP(w, r)
	})
	return withLogging(withRecovery(withRequestID(withCORS(root))))
}

// ListenAndServe starts the HTTP listener. Blocks until stopped.
func (s *Server) ListenAndServe(addr string) error {
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.handler(),
	}
	return s.server.ListenAndServe()
}

// Serve accepts connections on lis. Blocks until stopped.
// Use this with a pre-created listener for synchronous bind validation.
func (s *Server) Serve(lis net.Listener) error {
	s.server = &http.Server{
		Handler: s.handler(),
	}
	return s.server.Serve(lis)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) registerRoutes() {
	// WebSocket — primary API transport. All domain operations go through WS.
	s.mux.HandleFunc("GET /v0/ws", s.handleWebSocket)

	// HTTP-only survivors (justified operational endpoints):
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /v0/readiness", handleReadiness)
	s.mux.HandleFunc("GET /v0/provider-readiness", handleProviderReadiness)
	s.mux.HandleFunc("POST /v0/city", handleCityCreate)

	// API specs — self-documenting endpoints.
	s.mux.HandleFunc("GET /v0/asyncapi.yaml", handleAsyncAPISpec)
	s.mux.HandleFunc("GET /v0/openapi.yaml", handleOpenAPISpec)

	// Workspace service proxy — HTTP passthrough to backend services.
	s.mux.HandleFunc("/svc/", s.handleServiceProxy)

	// All other API endpoints are WS-only via GET /v0/ws.
}
