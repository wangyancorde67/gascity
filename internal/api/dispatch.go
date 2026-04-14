package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"sync"
)

// ActionDef holds the metadata for registering a WebSocket action.
// Used by RegisterAction and RegisterVoidAction.
type ActionDef struct {
	Description       string
	IsMutation        bool
	RequiresCityScope bool
	SupportsWatch     bool
}

// actionHandler is the raw dispatch signature used internally by the framework.
type actionHandler func(s *Server, req *socketRequestEnvelope) (socketActionResult, *socketErrorEnvelope)

// actionEntry combines metadata with a runtime handler. The RequestType and
// ResponseType fields are populated by the generic RegisterAction helper so the
// spec generator can reflect on them without importing handler-specific types.
type actionEntry struct {
	Name              string
	Description       string
	IsMutation        bool
	RequiresCityScope bool
	SupportsWatch     bool
	RequestType       reflect.Type // nil for void actions
	ResponseType      reflect.Type
	Handler           actionHandler // nil = legacy fallback during migration
}

var (
	actionTableMu sync.Mutex
	actionTable   = map[string]*actionEntry{}
)

// RegisterAction registers a typed action handler. The generic In/Out types
// drive both runtime dispatch (JSON decode/encode) AND spec generation (the
// reflect.Type is fed to the swaggest reflector). Handlers are pure business
// logic — they never see envelopes.
func RegisterAction[In, Out any](name string, def ActionDef, handler func(*Server, In) (Out, error)) {
	actionTableMu.Lock()
	defer actionTableMu.Unlock()
	actionTable[name] = &actionEntry{
		Name:              name,
		Description:       def.Description,
		IsMutation:        def.IsMutation,
		RequiresCityScope: def.RequiresCityScope,
		SupportsWatch:     def.SupportsWatch,
		RequestType:       reflect.TypeOf((*In)(nil)).Elem(),
		ResponseType:      reflect.TypeOf((*Out)(nil)).Elem(),
		Handler: func(s *Server, req *socketRequestEnvelope) (socketActionResult, *socketErrorEnvelope) {
			var input In
			if len(req.Payload) > 0 {
				if err := json.Unmarshal(req.Payload, &input); err != nil {
					return socketActionResult{}, newSocketError(req.ID, "invalid", err.Error())
				}
			}
			result, err := handler(s, input)
			if err != nil {
				return socketActionResult{}, socketErrorFor(req.ID, err)
			}
			return socketActionResult{Result: result, Index: s.latestIndex()}, nil
		},
	}
}

// RegisterVoidAction registers an action that takes no payload.
func RegisterVoidAction[Out any](name string, def ActionDef, handler func(*Server) (Out, error)) {
	actionTableMu.Lock()
	defer actionTableMu.Unlock()
	actionTable[name] = &actionEntry{
		Name:              name,
		Description:       def.Description,
		IsMutation:        def.IsMutation,
		RequiresCityScope: def.RequiresCityScope,
		SupportsWatch:     def.SupportsWatch,
		ResponseType:      reflect.TypeOf((*Out)(nil)).Elem(),
		Handler: func(s *Server, req *socketRequestEnvelope) (socketActionResult, *socketErrorEnvelope) {
			result, err := handler(s)
			if err != nil {
				return socketActionResult{}, socketErrorFor(req.ID, err)
			}
			return socketActionResult{Result: result, Index: s.latestIndex()}, nil
		},
	}
}

// RegisterMeta registers action metadata without a handler. Used during
// incremental migration — the action appears in capabilities and spec
// but dispatch falls back to the legacy switch.
func RegisterMeta(name string, def ActionDef) {
	actionTableMu.Lock()
	defer actionTableMu.Unlock()
	actionTable[name] = &actionEntry{
		Name:              name,
		Description:       def.Description,
		IsMutation:        def.IsMutation,
		RequiresCityScope: def.RequiresCityScope,
		SupportsWatch:     def.SupportsWatch,
	}
}

// dispatchAction looks up the action in the table and dispatches it.
// Falls back to the legacy switch during incremental migration.
func (s *Server) dispatchAction(req *socketRequestEnvelope) (socketActionResult, *socketErrorEnvelope) {
	entry, ok := actionTable[req.Action]
	if !ok || entry.Handler == nil {
		// Not yet migrated — fall back to legacy switch.
		return s.handleSocketRequestLegacy(req)
	}
	// On per-city servers, validate that scope.city matches (or is absent).
	if req.Scope != nil && req.Scope.City != "" {
		if cityName := s.state.CityName(); req.Scope.City != cityName {
			return socketActionResult{}, newSocketError(req.ID, "invalid",
				"scope.city "+req.Scope.City+" does not match this city "+cityName)
		}
	}
	if s.readOnly && entry.IsMutation {
		return socketActionResult{}, newSocketError(req.ID, "read_only",
			"mutations disabled: server bound to non-localhost address")
	}
	return entry.Handler(s, req)
}

// actionTableCapabilities returns sorted action names for the hello envelope.
func actionTableCapabilities() []string {
	caps := make([]string, 0, len(actionTable))
	for name := range actionTable {
		caps = append(caps, name)
	}
	sort.Strings(caps)
	return caps
}

// actionTableRequiresCityScope checks whether an action requires city scope.
func actionTableRequiresCityScope(action string) bool {
	if entry, ok := actionTable[action]; ok {
		return entry.RequiresCityScope
	}
	return false
}

// actionTableSupportsWatch checks whether an action supports blocking queries.
func actionTableSupportsWatch(action string) bool {
	if entry, ok := actionTable[action]; ok {
		return entry.SupportsWatch
	}
	return false
}
