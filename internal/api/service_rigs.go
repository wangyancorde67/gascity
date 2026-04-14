package api

// RigService is the domain interface for rig operations.
type RigService interface {
	List(wantGit bool) []rigResponse
	Get(name string, wantGit bool) (rigResponse, bool)
	ApplyAction(name, action string) (map[string]any, error)
}

// rigService is the default RigService implementation.
type rigService struct {
	s *Server
}

func (r *rigService) List(wantGit bool) []rigResponse {
	return r.s.listRigResponses(wantGit)
}

func (r *rigService) Get(name string, wantGit bool) (rigResponse, bool) {
	return r.s.getRigResponse(name, wantGit)
}

func (r *rigService) ApplyAction(name, action string) (map[string]any, error) {
	return r.s.applyRigAction(name, action)
}
