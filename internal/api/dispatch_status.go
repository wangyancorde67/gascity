package api

func init() {
	RegisterVoidAction("health.get", ActionDef{
		Description: "Health check",
	}, func(s *Server) (map[string]any, error) {
		return s.healthResponse(), nil
	})

	RegisterVoidAction("status.get", ActionDef{
		Description:       "City status snapshot",
		RequiresCityScope: true,
		SupportsWatch:     true,
	}, func(s *Server) (any, error) {
		return s.statusSnapshot(), nil
	})
}
