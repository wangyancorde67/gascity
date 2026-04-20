package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/sessionlog"
)

func seedSessionAgentTranscript(t *testing.T, fs *fakeState) (string, string) {
	t.Helper()

	workDir := t.TempDir()
	sessionID := "gc-123"

	searchBase := t.TempDir()
	logDir := filepath.Join(searchBase, sessionlog.ProjectSlug(workDir))
	if err := os.MkdirAll(filepath.Join(logDir, sessionID, "subagents"), 0o755); err != nil {
		t.Fatalf("mkdir transcript dirs: %v", err)
	}

	parentLog := filepath.Join(logDir, sessionID+".jsonl")
	if err := os.WriteFile(parentLog, []byte(`{"uuid":"p1","type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write parent transcript: %v", err)
	}

	agentLog := filepath.Join(logDir, sessionID, "subagents", "agent-myagent.jsonl")
	agentContent := `{"uuid":"a1","type":"system","parentToolUseId":"toolu_123"}` + "\n" +
		`{"uuid":"a2","parentUuid":"a1","type":"assistant","message":{"role":"assistant","content":"working"}}` + "\n" +
		`{"uuid":"a3","parentUuid":"a2","type":"result","message":{"role":"result"}}` + "\n"
	if err := os.WriteFile(agentLog, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("write agent transcript: %v", err)
	}

	sess, err := fs.cityBeadStore.Create(beads.Bead{
		Type:   session.BeadType,
		Title:  "worker",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"session_name": "worker",
			"work_dir":     workDir,
			"provider":     "claude",
		},
	})
	if err != nil {
		t.Fatalf("create session bead in city store: %v", err)
	}

	return sess.ID, searchBase
}

func TestHandleSessionAgentList(t *testing.T) {
	fs := newSessionFakeState(t)
	sessionID, searchBase := seedSessionAgentTranscript(t, fs)
	srv := New(fs)
	srv.sessionLogSearchPaths = []string{searchBase}

	req := httptest.NewRequest("GET", "/v0/session/"+sessionID+"/agents", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Agents []sessionlog.AgentMapping `json:"agents"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(resp.Agents))
	}
	if resp.Agents[0].AgentID != "myagent" {
		t.Fatalf("agent_id = %q, want myagent", resp.Agents[0].AgentID)
	}
	if resp.Agents[0].ParentToolUseID != "toolu_123" {
		t.Fatalf("parent_tool_use_id = %q, want toolu_123", resp.Agents[0].ParentToolUseID)
	}
}

func TestHandleSessionAgentGet(t *testing.T) {
	fs := newSessionFakeState(t)
	sessionID, searchBase := seedSessionAgentTranscript(t, fs)
	srv := New(fs)
	srv.sessionLogSearchPaths = []string{searchBase}

	req := httptest.NewRequest("GET", "/v0/session/"+sessionID+"/agents/myagent", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []json.RawMessage `json:"messages"`
		Status   string            `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("status = %q, want completed", resp.Status)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(resp.Messages))
	}
}
