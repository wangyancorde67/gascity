package api

import (
	"context"

	"github.com/gastownhall/gascity/internal/config"
)

type socketAgentsListPayload struct {
	Pool    string `json:"pool,omitempty"`
	Rig     string `json:"rig,omitempty"`
	Running string `json:"running,omitempty"`
	Peek    bool   `json:"peek,omitempty"`
}

type socketAgentUpdatePayload struct {
	Name      string `json:"name"`
	Provider  string `json:"provider,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Suspended *bool  `json:"suspended,omitempty"`
}

func init() {
	RegisterAction("agents.list", ActionDef{
		Description:       "List agents",
		RequiresCityScope: true,
		SupportsWatch:     true,
	}, func(_ context.Context, s *Server, payload socketAgentsListPayload) (listResponse, error) {
		items := s.Agents.List(payload.Pool, payload.Rig, payload.Running, payload.Peek)
		return listResponse{Items: items, Total: len(items)}, nil
	})

	RegisterAction("agent.get", ActionDef{
		Description:       "Get agent details",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketNamePayload) (any, error) {
		cfg := s.state.Config()
		agentCfg, ok := findAgent(cfg, payload.Name)
		if !ok {
			return nil, httpError{status: 404, code: "not_found", message: "agent " + payload.Name + " not found"}
		}
		resp, _ := s.Agents.BuildExpandedResponse(agentCfg, expandedAgent{
			qualifiedName: payload.Name,
			rig:           agentCfg.Dir,
			suspended:     agentCfg.Suspended,
			provider:      agentCfg.Provider,
			description:   agentCfg.Description,
		}, false, "")
		return resp, nil
	})

	RegisterAction("agent.suspend", ActionDef{
		Description:       "Suspend an agent",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketNamePayload) (map[string]string, error) {
		if err := s.Agents.ApplyAction(payload.Name,"suspend"); err != nil {
			return nil, err
		}
		return map[string]string{"status": "ok"}, nil
	})

	RegisterAction("agent.resume", ActionDef{
		Description:       "Resume an agent",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketNamePayload) (map[string]string, error) {
		if err := s.Agents.ApplyAction(payload.Name,"resume"); err != nil {
			return nil, err
		}
		return map[string]string{"status": "ok"}, nil
	})

	RegisterAction("agent.create", ActionDef{
		Description:       "Create an agent",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload agentCreateRequest) (map[string]string, error) {
		sm, ok := s.state.(StateMutator)
		if !ok {
			return nil, httpError{status: 500, code: "internal", message: "mutations not supported"}
		}
		if payload.Name == "" {
			return nil, httpError{status: 400, code: "invalid", message: "name is required"}
		}
		if payload.Provider == "" {
			return nil, httpError{status: 400, code: "invalid", message: "provider is required"}
		}
		a := config.Agent{Name: payload.Name, Dir: payload.Dir, Provider: payload.Provider, Scope: payload.Scope}
		if err := sm.CreateAgent(a); err != nil {
			return nil, err
		}
		return map[string]string{"status": "created", "agent": a.QualifiedName()}, nil
	})

	RegisterAction("agent.update", ActionDef{
		Description:       "Update an agent",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketAgentUpdatePayload) (map[string]string, error) {
		sm, ok := s.state.(StateMutator)
		if !ok {
			return nil, httpError{status: 500, code: "internal", message: "mutations not supported"}
		}
		if err := sm.UpdateAgent(payload.Name, AgentUpdate{Provider: payload.Provider, Scope: payload.Scope, Suspended: payload.Suspended}); err != nil {
			return nil, err
		}
		return map[string]string{"status": "updated", "agent": payload.Name}, nil
	})

	RegisterAction("agent.delete", ActionDef{
		Description:       "Delete an agent",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketNamePayload) (map[string]string, error) {
		sm, ok := s.state.(StateMutator)
		if !ok {
			return nil, httpError{status: 500, code: "internal", message: "mutations not supported"}
		}
		if err := sm.DeleteAgent(payload.Name); err != nil {
			return nil, err
		}
		return map[string]string{"status": "deleted", "agent": payload.Name}, nil
	})
}
