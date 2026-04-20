package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// processKillGracePeriod is how long to wait after SIGTERM before sending SIGKILL.
// 2 seconds gives processes time to clean up gracefully. The previous 100ms was too short
// and caused Claude processes to become orphans when they couldn't shut down in time.
const processKillGracePeriod = 2 * time.Second

// KillSessionWithProcesses explicitly kills all processes in a session before terminating it.
// This prevents orphan processes that survive tmux kill-session due to SIGHUP being ignored.
func (t *Tmux) KillSessionWithProcesses(name string) error {
	pid, err := t.GetPanePID(name)
	if err != nil {
		killErr := t.KillSession(name)
		if killErr == nil || errors.Is(killErr, ErrSessionNotFound) || errors.Is(killErr, ErrNoServer) {
			return nil
		}
		return killErr
	}

	if pid != "" {
		killProcessTree(pid, nil)
	}

	err = t.KillSession(name)
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
		return nil
	}
	return err
}

// KillSessionWithProcessesExcluding is like KillSessionWithProcesses but excludes
// specified PIDs from being killed. This is essential for self-kill scenarios where
// the calling process (e.g., gt done) is running inside the session it's terminating.
// Without exclusion, the caller would be killed before completing the cleanup.
func (t *Tmux) KillSessionWithProcessesExcluding(name string, excludePIDs []string) error {
	exclude := make(map[string]bool, len(excludePIDs))
	for _, pid := range excludePIDs {
		exclude[pid] = true
	}

	pid, err := t.GetPanePID(name)
	if err != nil {
		killErr := t.KillSession(name)
		if killErr == nil || errors.Is(killErr, ErrSessionNotFound) || errors.Is(killErr, ErrNoServer) {
			return nil
		}
		return killErr
	}

	if pid != "" {
		killProcessTree(pid, exclude)
	}

	err = t.KillSession(name)
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
		return nil
	}
	return err
}

// KillPaneProcesses explicitly kills all processes associated with a tmux pane.
// This prevents orphan processes that survive pane respawn due to SIGHUP being ignored.
func (t *Tmux) KillPaneProcesses(pane string) error {
	pid, err := t.GetPanePID(pane)
	if err != nil {
		return fmt.Errorf("getting pane PID: %w", err)
	}
	if pid == "" {
		return fmt.Errorf("pane PID is empty")
	}

	killProcessTree(pid, nil)
	return nil
}

// KillPaneProcessesExcluding is like KillPaneProcesses but excludes specified PIDs
// from being killed. This is essential for self-handoff scenarios where the calling
// process (e.g., gt handoff running inside Claude Code) needs to survive long enough
// to call RespawnPane.
func (t *Tmux) KillPaneProcessesExcluding(pane string, excludePIDs []string) error {
	exclude := make(map[string]bool, len(excludePIDs))
	for _, pid := range excludePIDs {
		exclude[pid] = true
	}

	pid, err := t.GetPanePID(pane)
	if err != nil {
		return fmt.Errorf("getting pane PID: %w", err)
	}
	if pid == "" {
		return fmt.Errorf("pane PID is empty")
	}

	killProcessTree(pid, exclude)
	return nil
}

func killProcessTree(rootPID string, exclude map[string]bool) {
	descendants := getAllDescendants(rootPID)
	knownPIDs := make(map[string]bool, len(descendants)+1)
	knownPIDs[rootPID] = true

	var killList []string
	for _, pid := range descendants {
		knownPIDs[pid] = true
		if exclude == nil || !exclude[pid] {
			killList = append(killList, pid)
		}
	}

	pgid := getProcessGroupID(rootPID)
	if pgid != "" && pgid != "0" && pgid != "1" {
		for _, pid := range collectReparentedGroupMembers(pgid, knownPIDs) {
			if exclude != nil && exclude[pid] {
				continue
			}
			killList = append(killList, pid)
		}
	}

	signalPIDs(killList, "TERM")
	time.Sleep(processKillGracePeriod)
	signalPIDs(killList, "KILL")

	if exclude == nil || !exclude[rootPID] {
		signalPID(rootPID, "TERM")
		time.Sleep(processKillGracePeriod)
		signalPID(rootPID, "KILL")
	}
}

func signalPIDs(pids []string, signal string) {
	for _, pid := range pids {
		signalPID(pid, signal)
	}
}

func signalPID(pid, signal string) {
	_ = exec.Command("kill", "-"+signal, pid).Run()
}

// collectReparentedGroupMembers returns process group members that have been
// reparented to init (PPID == 1) but are not in the known descendant set.
func collectReparentedGroupMembers(pgid string, knownPIDs map[string]bool) []string {
	members := getProcessGroupMembers(pgid)
	var reparented []string
	for _, member := range members {
		if knownPIDs[member] {
			continue
		}
		if getParentPID(member) == "1" {
			reparented = append(reparented, member)
		}
	}
	return reparented
}

// getAllDescendants recursively finds all descendant PIDs of a process.
// Returns PIDs in deepest-first order so killing them doesn't orphan grandchildren.
func getAllDescendants(pid string) []string {
	var result []string

	out, err := exec.Command("pgrep", "-P", pid).Output()
	if err != nil {
		return result
	}

	children := strings.Fields(strings.TrimSpace(string(out)))
	for _, child := range children {
		result = append(result, getAllDescendants(child)...)
		result = append(result, child)
	}

	return result
}
