package api

import (
	"github.com/gastownhall/gascity/internal/config"
)

// AgentService is the domain interface for agent operations.
type AgentService interface {
	List(qPool, qRig, qRunning string, wantPeek bool) []agentResponse
	BuildExpandedResponse(agentCfg config.Agent, ea expandedAgent, wantPeek bool, qRunning string) (agentResponse, bool)
	ApplyAction(name, action string) error
}

// agentService is the default AgentService implementation.
type agentService struct {
	s *Server
}

func (a *agentService) List(qPool, qRig, qRunning string, wantPeek bool) []agentResponse {
	return a.s.listAgentResponses(qPool, qRig, qRunning, wantPeek)
}

func (a *agentService) BuildExpandedResponse(agentCfg config.Agent, ea expandedAgent, wantPeek bool, qRunning string) (agentResponse, bool) {
	return a.s.buildExpandedAgentResponse(agentCfg, ea, wantPeek, qRunning)
}

func (a *agentService) ApplyAction(name, action string) error {
	return a.s.applyAgentAction(name, action)
}
