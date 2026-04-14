package api

// ProviderService is the domain interface for provider operations.
type ProviderService interface {
	List(isPublic bool) []any
	Get(name string) (providerResponse, error)
}

// providerService is the default ProviderService implementation.
type providerService struct {
	s *Server
}

func (p *providerService) List(isPublic bool) []any {
	return p.s.listProviders(isPublic)
}

func (p *providerService) Get(name string) (providerResponse, error) {
	return p.s.getProvider(name)
}
