package api

import "context"

// FormulaService is the domain interface for formula operations.
type FormulaService interface {
	List(scopeKind, scopeRef string) (any, error)
	Feed(scopeKind, scopeRef string, limit int) (any, error)
	Get(ctx context.Context, name, scopeKind, scopeRef, target string, vars map[string]string) (any, error)
	Runs(name, scopeKind, scopeRef string, limit int) (any, error)
}

// formulaService is the default FormulaService implementation.
type formulaService struct {
	s *Server
}

func (f *formulaService) List(scopeKind, scopeRef string) (any, error) {
	return f.s.listFormulas(scopeKind, scopeRef)
}

func (f *formulaService) Feed(scopeKind, scopeRef string, limit int) (any, error) {
	return f.s.getFormulaFeed(scopeKind, scopeRef, limit)
}

func (f *formulaService) Get(ctx context.Context, name, scopeKind, scopeRef, target string, vars map[string]string) (any, error) {
	return f.s.getFormulaDetail(ctx, name, scopeKind, scopeRef, target, vars)
}

func (f *formulaService) Runs(name, scopeKind, scopeRef string, limit int) (any, error) {
	return f.s.getFormulaRuns(name, scopeKind, scopeRef, limit)
}
