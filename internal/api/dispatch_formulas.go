package api

import "context"

type socketFormulaScopePayload struct {
	ScopeKind string `json:"scope_kind"`
	ScopeRef  string `json:"scope_ref"`
}

type socketFormulaFeedPayload struct {
	ScopeKind string `json:"scope_kind"`
	ScopeRef  string `json:"scope_ref"`
	Limit     int    `json:"limit,omitempty"`
}

type socketFormulaGetPayload struct {
	Name      string            `json:"name"`
	ScopeKind string            `json:"scope_kind"`
	ScopeRef  string            `json:"scope_ref"`
	Target    string            `json:"target"`
	Vars      map[string]string `json:"vars,omitempty"`
}

type socketFormulaRunsPayload struct {
	Name      string `json:"name"`
	ScopeKind string `json:"scope_kind"`
	ScopeRef  string `json:"scope_ref"`
	Limit     int    `json:"limit,omitempty"`
}

func init() {
	RegisterAction("formulas.list", ActionDef{
		Description:       "List formulas",
		RequiresCityScope: true,
		SupportsWatch:     true,
	}, func(_ context.Context, s *Server, payload socketFormulaScopePayload) (any, error) {
		return s.Formulas.List(payload.ScopeKind, payload.ScopeRef)
	})

	RegisterAction("formulas.feed", ActionDef{
		Description:       "Get formula activity feed",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketFormulaFeedPayload) (any, error) {
		return s.Formulas.Feed(payload.ScopeKind, payload.ScopeRef, payload.Limit)
	})

	RegisterAction("formula.get", ActionDef{
		Description:       "Get formula details",
		RequiresCityScope: true,
	}, func(ctx context.Context, s *Server, payload socketFormulaGetPayload) (any, error) {
		return s.Formulas.Get(ctx, payload.Name, payload.ScopeKind, payload.ScopeRef, payload.Target, payload.Vars)
	})

	RegisterAction("formula.runs", ActionDef{
		Description:       "Get formula run history",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketFormulaRunsPayload) (any, error) {
		return s.Formulas.Runs(payload.Name, payload.ScopeKind, payload.ScopeRef, payload.Limit)
	})
}
