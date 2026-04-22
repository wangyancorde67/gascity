package api

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/materialize"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	workdirutil "github.com/gastownhall/gascity/internal/workdir"
	"github.com/gastownhall/gascity/internal/worker"
)

func (s *Server) sessionLogPaths() []string {
	if s.sessionLogSearchPaths != nil {
		return s.sessionLogSearchPaths
	}
	cfg := s.state.Config()
	if cfg == nil {
		return worker.DefaultSearchPaths()
	}
	return worker.MergeSearchPaths(cfg.Daemon.ObservePaths)
}

func sessionCreateHints(resolved *config.ResolvedProvider, mcpServers []runtime.MCPServerConfig) runtime.Config {
	return runtime.Config{
		ReadyPromptPrefix:      resolved.ReadyPromptPrefix,
		ReadyDelayMs:           resolved.ReadyDelayMs,
		ProcessNames:           resolved.ProcessNames,
		EmitsPermissionWarning: resolved.EmitsPermissionWarning,
		MCPServers:             mcpServers,
	}
}

func sessionResumeHints(resolved *config.ResolvedProvider, workDir string, mcpServers []runtime.MCPServerConfig) runtime.Config {
	return runtime.Config{
		WorkDir:                workDir,
		ReadyPromptPrefix:      resolved.ReadyPromptPrefix,
		ReadyDelayMs:           resolved.ReadyDelayMs,
		ProcessNames:           resolved.ProcessNames,
		EmitsPermissionWarning: resolved.EmitsPermissionWarning,
		Env:                    resolved.Env,
		MCPServers:             mcpServers,
	}
}

func resumeSessionIdentity(info session.Info) string {
	return firstNonEmptyString(info.AgentName, info.Alias, info.Template, info.Provider)
}

func (s *Server) resumeSessionMCPServers(info session.Info, resolved *config.ResolvedProvider, workDir, transport string) []runtime.MCPServerConfig {
	if resolved == nil {
		return nil
	}
	// Existing ACP sessions resume from their stored session state. Current
	// MCP catalog materialization only seeds session/new and should not strand
	// already-created sessions if the catalog on disk is currently broken.
	mcpServers, err := s.sessionMCPServers(
		info.Template,
		firstNonEmptyString(info.Provider, resolved.Name),
		resumeSessionIdentity(info),
		workDir,
		transport,
		s.sessionKind(info.ID),
	)
	if err != nil {
		return nil
	}
	return mcpServers
}

func (s *Server) providerSessionMCPServers(providerName, workDir, transport string) ([]runtime.MCPServerConfig, error) {
	cfg := s.state.Config()
	if cfg == nil || strings.TrimSpace(workDir) == "" || strings.TrimSpace(transport) != "acp" {
		return nil, nil
	}
	synthetic := &config.Agent{Provider: providerName}
	catalog, err := materialize.EffectiveMCPForSession(cfg, s.state.CityPath(), synthetic, providerName, workDir)
	if err != nil {
		return nil, fmt.Errorf("loading effective MCP: %w", err)
	}
	return materialize.RuntimeMCPServers(catalog.Servers), nil
}

func (s *Server) sessionMCPServers(template, providerName, identity, workDir, transport, sessionKind string) ([]runtime.MCPServerConfig, error) {
	cfg := s.state.Config()
	if cfg == nil || strings.TrimSpace(workDir) == "" || strings.TrimSpace(transport) != "acp" {
		return nil, nil
	}
	if sessionKind != "provider" {
		if agentCfg, ok := resolveSessionTemplateAgent(cfg, template); ok {
			catalog, err := materialize.EffectiveMCPForSession(
				cfg,
				s.state.CityPath(),
				&agentCfg,
				firstNonEmptyString(identity, template),
				workDir,
			)
			if err != nil {
				return nil, fmt.Errorf("loading effective MCP: %w", err)
			}
			return materialize.RuntimeMCPServers(catalog.Servers), nil
		}
	}
	return s.providerSessionMCPServers(firstNonEmptyString(providerName, template), workDir, transport)
}

func sessionExplicitNameForCreate(agentCfg config.Agent, alias string) (string, error) {
	if !agentCfg.SupportsMultipleSessions() || strings.TrimSpace(alias) != "" {
		return "", nil
	}
	return session.GenerateAdhocExplicitName(agentCfg.Name)
}

func (s *Server) resolveSessionWorkDir(agentCfg config.Agent, qualifiedName string) (string, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return "", errors.New("no city config loaded")
	}
	workDir, err := workdirutil.ResolveWorkDirPathStrict(
		s.state.CityPath(),
		workdirutil.CityName(s.state.CityPath(), cfg),
		qualifiedName,
		agentCfg,
		cfg.Rigs,
	)
	if err != nil {
		return "", err
	}
	if workDir == "" {
		workDir = s.state.CityPath()
	}
	return workDir, nil
}

// resolveSessionTemplateWithBareNameFallback resolves a session template
// by name, retrying with the qualified name when the input is a bare
// agent name that matches exactly one configured agent. Keeps the
// two-phase lookup out of the handler.
func (s *Server) resolveSessionTemplateWithBareNameFallback(name string) (*config.ResolvedProvider, string, string, string, error) {
	resolved, workDir, transport, template, err := s.resolveSessionTemplate(name)
	if err == nil {
		return resolved, workDir, transport, template, nil
	}
	if !errors.Is(err, errSessionTemplateNotFound) || strings.Contains(name, "/") {
		return nil, "", "", "", err
	}
	agentCfg, ok := findUniqueAgentTemplateByBareName(s.state.Config(), name)
	if !ok {
		return nil, "", "", "", err
	}
	return s.resolveSessionTemplate(agentCfg.QualifiedName())
}

func (s *Server) resolveSessionTemplate(template string) (*config.ResolvedProvider, string, string, string, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return nil, "", "", "", errors.New("no city config loaded")
	}
	agentCfg, ok := resolveSessionTemplateAgent(cfg, template)
	if !ok {
		return nil, "", "", "", errSessionTemplateNotFound
	}
	resolved, err := config.ResolveProvider(&agentCfg, &cfg.Workspace, cfg.Providers, exec.LookPath)
	if err != nil {
		return nil, "", "", "", err
	}
	workDir, err := s.resolveSessionWorkDir(agentCfg, agentCfg.QualifiedName())
	if err != nil {
		return nil, "", "", "", err
	}
	return resolved, workDir, agentCfg.Session, agentCfg.QualifiedName(), nil
}

func (s *Server) buildSessionResume(info session.Info) (string, runtime.Config, error) {
	cmd := session.BuildResumeCommand(info)
	resolved, workDir, transport := s.resolveSessionRuntime(info)
	if resolved == nil {
		return cmd, runtime.Config{WorkDir: info.WorkDir}, nil
	}
	mcpServers := s.resumeSessionMCPServers(info, resolved, firstNonEmptyString(workDir, info.WorkDir), transport)
	resolvedInfo := info
	if command, err := s.resolvedSessionRuntimeCommand(resolved, transport, info.Command); err == nil {
		resolvedInfo.Command = command
	} else {
		resolvedCommand := resolved.CommandString()
		if transport == "acp" {
			resolvedCommand = resolved.ACPCommandString()
		}
		resolvedInfo.Command = firstNonEmptyString(info.Command, resolvedCommand, resolved.Name)
	}
	resolvedInfo.Provider = resolved.Name
	resolvedInfo.Transport = transport
	resolvedInfo.ResumeFlag = resolved.ResumeFlag
	resolvedInfo.ResumeStyle = resolved.ResumeStyle
	resolvedInfo.ResumeCommand = resolved.ResumeCommand
	return session.BuildResumeCommand(resolvedInfo), sessionResumeHints(resolved, workDir, mcpServers), nil
}

func (s *Server) resolvedSessionRuntimeCommand(resolved *config.ResolvedProvider, transport, storedCommand string) (string, error) {
	resolvedCommand := resolved.CommandString()
	if transport == "acp" {
		resolvedCommand = resolved.ACPCommandString()
	}
	if command := strings.TrimSpace(storedCommand); shouldPreserveStoredRuntimeCommand(command, resolvedCommand) {
		return command, nil
	}
	launchCommand, err := config.BuildProviderLaunchCommand(s.state.CityPath(), resolved, nil, transport)
	if err != nil {
		return "", fmt.Errorf("building provider launch command: %w", err)
	}
	return firstNonEmptyString(launchCommand.Command, resolvedCommand, resolved.Name), nil
}

func shouldPreserveStoredRuntimeCommand(storedCommand, resolvedCommand string) bool {
	storedCommand = strings.TrimSpace(storedCommand)
	if storedCommand == "" {
		return false
	}
	resolvedCommand = strings.TrimSpace(resolvedCommand)
	if resolvedCommand == "" {
		return true
	}
	// A bare stored command (just the provider binary) lacks schema
	// defaults like --dangerously-skip-permissions and the --settings
	// path. Rebuild from the current config instead of preserving it.
	// See #799: pool-agent sessions resumed through the control-
	// dispatcher path wedged on interactive permission prompts because
	// the bare stored command was preserved without re-injecting flags.
	if storedCommand == resolvedCommand {
		return false
	}
	return strings.HasPrefix(storedCommand, resolvedCommand+" ")
}

func (s *Server) resolveWorkerSessionRuntime(info session.Info, _ string) (*worker.ResolvedRuntime, error) {
	resolved, workDir, transport := s.resolveSessionRuntime(info)
	if resolved == nil {
		return nil, nil
	}
	mcpServers := s.resumeSessionMCPServers(info, resolved, firstNonEmptyString(workDir, info.WorkDir), transport)
	command, err := s.resolvedSessionRuntimeCommand(resolved, transport, info.Command)
	if err != nil {
		return nil, err
	}
	runtimeCfg, err := worker.NormalizeResolvedRuntime(worker.ResolvedRuntime{
		Command:    command,
		WorkDir:    firstNonEmptyString(info.WorkDir, workDir),
		Provider:   firstNonEmptyString(info.Provider, resolved.Name),
		SessionEnv: resolved.Env,
		Hints:      sessionResumeHints(resolved, firstNonEmptyString(workDir, info.WorkDir), mcpServers),
		Resume: session.ProviderResume{
			ResumeFlag:    firstNonEmptyString(resolved.ResumeFlag, info.ResumeFlag),
			ResumeStyle:   firstNonEmptyString(resolved.ResumeStyle, info.ResumeStyle),
			ResumeCommand: firstNonEmptyString(resolved.ResumeCommand, info.ResumeCommand),
			SessionIDFlag: resolved.SessionIDFlag,
		},
	})
	if err != nil {
		return nil, err
	}
	return &runtimeCfg, nil
}

func (s *Server) resolveSessionRuntime(info session.Info) (*config.ResolvedProvider, string, string) {
	kind := s.sessionKind(info.ID)
	if kind != "provider" {
		resolved, workDir, transport, _, err := s.resolveSessionTemplate(info.Template)
		if err == nil {
			if info.WorkDir != "" {
				workDir = info.WorkDir
			}
			return resolved, workDir, firstNonEmptyString(strings.TrimSpace(info.Transport), strings.TrimSpace(transport))
		}
	}

	resolved, err := s.resolveBareProvider(info.Template)
	if err != nil {
		return nil, "", ""
	}
	workDir := info.WorkDir
	if workDir == "" {
		workDir = s.state.CityPath()
	}
	transport := firstNonEmptyString(strings.TrimSpace(info.Transport), strings.TrimSpace(resolved.DefaultSessionTransport()))
	return resolved, workDir, transport
}

// sessionKind reads the persisted mc_session_kind from bead metadata.
func (s *Server) sessionKind(sessionID string) string {
	store := s.state.CityBeadStore()
	if store == nil {
		return ""
	}
	b, err := store.Get(sessionID)
	if err != nil {
		return ""
	}
	return b.Metadata["mc_session_kind"]
}

// resolveBareProvider resolves a provider by name without an agent template.
func (s *Server) resolveBareProvider(providerName string) (*config.ResolvedProvider, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return nil, errors.New("no city config loaded")
	}
	return config.ResolveProvider(
		&config.Agent{Provider: providerName},
		&cfg.Workspace,
		cfg.Providers,
		exec.LookPath,
	)
}
