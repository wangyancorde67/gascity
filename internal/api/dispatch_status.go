package api

import "context"

func init() {
	RegisterMeta("cities.list", ActionDef{
		Description: "List managed cities (supervisor)",
		ServerRoles: actionServerRoleSupervisor,
	})

	RegisterVoidAction("health.get", ActionDef{
		Description: "Health check",
	}, func(_ context.Context, s *Server) (map[string]any, error) {
		return s.healthResponse(), nil
	})

	RegisterVoidAction("status.get", ActionDef{
		Description:       "City status snapshot",
		RequiresCityScope: true,
		SupportsWatch:     true,
	}, func(_ context.Context, s *Server) (any, error) {
		return s.statusSnapshot(), nil
	})
}
