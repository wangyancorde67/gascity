package api

import (
	"context"

	"github.com/gastownhall/gascity/internal/session"
)

// SessionService is the domain interface for session operations.
type SessionService interface {
	List(stateFilter, templateFilter string, wantPeek bool) ([]sessionResponse, error)
	Get(identifier string, wantPeek bool) (sessionResponse, error)
	Create(ctx context.Context, body sessionCreateRequest, idemKey string) (any, int, error)
	Suspend(identifier string) error
	Close(identifier string) error
	Wake(ctx context.Context, identifier string) (map[string]string, error)
	Rename(identifier, title string) (sessionResponse, error)
	Respond(identifier string, body sessionRespondRequest) (map[string]string, error)
	Kill(target string) (map[string]string, error)
	Pending(target string) (sessionPendingResponse, error)
	Submit(ctx context.Context, target, message string, intent session.SubmitIntent) (map[string]any, error)
	Transcript(target string, query sessionTranscriptQuery) (any, error)
	Patch(target string, title, alias *string) (any, error)
	ListAgents(target string) (any, error)
	GetAgent(target, agentID string) (any, error)
}

// sessionService is the default SessionService implementation.
type sessionService struct {
	s *Server
}

func (ss *sessionService) List(stateFilter, templateFilter string, wantPeek bool) ([]sessionResponse, error) {
	return ss.s.listSessionResponses(stateFilter, templateFilter, wantPeek)
}

func (ss *sessionService) Get(identifier string, wantPeek bool) (sessionResponse, error) {
	return ss.s.getSessionResponse(identifier, wantPeek)
}

func (ss *sessionService) Create(ctx context.Context, body sessionCreateRequest, idemKey string) (any, int, error) {
	return ss.s.createSessionInternal(ctx, body, idemKey)
}

func (ss *sessionService) Suspend(identifier string) error {
	return ss.s.suspendSessionTarget(identifier)
}

func (ss *sessionService) Close(identifier string) error {
	return ss.s.closeSessionTarget(identifier)
}

func (ss *sessionService) Wake(ctx context.Context, identifier string) (map[string]string, error) {
	return ss.s.wakeSessionTarget(ctx, identifier)
}

func (ss *sessionService) Rename(identifier, title string) (sessionResponse, error) {
	return ss.s.renameSessionTarget(identifier, title)
}

func (ss *sessionService) Respond(identifier string, body sessionRespondRequest) (map[string]string, error) {
	return ss.s.respondSessionTarget(identifier, body)
}

func (ss *sessionService) Kill(target string) (map[string]string, error) {
	return ss.s.killSessionTarget(target)
}

func (ss *sessionService) Pending(target string) (sessionPendingResponse, error) {
	return ss.s.getSessionPending(target)
}

func (ss *sessionService) Submit(ctx context.Context, target, message string, intent session.SubmitIntent) (map[string]any, error) {
	return ss.s.submitSessionTarget(ctx, target, message, intent)
}

func (ss *sessionService) Transcript(target string, query sessionTranscriptQuery) (any, error) {
	return ss.s.getSessionTranscript(target, query)
}

func (ss *sessionService) Patch(target string, title, alias *string) (any, error) {
	return ss.s.patchSession(target, title, alias)
}

func (ss *sessionService) ListAgents(target string) (any, error) {
	return ss.s.listSessionAgents(target)
}

func (ss *sessionService) GetAgent(target, agentID string) (any, error) {
	return ss.s.getSessionAgent(target, agentID)
}
