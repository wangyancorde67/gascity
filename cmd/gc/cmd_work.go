package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/spf13/cobra"
)

var (
	resolveWorkCity = resolveCity
	openWorkStoreAt = openStoreAtForCity
	claimNextWorkFn = claimNextWork
)

func newWorkCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "work",
		Short: "Manage runnable work for agents",
	}
	cmd.AddCommand(newWorkClaimNextCmd(stdout, stderr))
	return cmd
}

func newWorkClaimNextCmd(stdout, stderr io.Writer) *cobra.Command {
	var template string
	var assignee string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "claim-next",
		Short: "Atomically claim the next routed work item",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(template) == "" {
				template = os.Getenv("GC_TEMPLATE")
			}
			if strings.TrimSpace(assignee) == "" {
				assignee = firstNonEmptyWorkEnv(os.Getenv("GC_SESSION_ID"), os.Getenv("GC_SESSION_NAME"), os.Getenv("GC_ALIAS"), os.Getenv("GC_AGENT"))
			}
			if strings.TrimSpace(template) == "" || strings.TrimSpace(assignee) == "" {
				return fmt.Errorf("claim-next requires --template and --assignee (or GC_TEMPLATE/GC_SESSION_ID env)")
			}
			cityPath, err := resolveWorkCity()
			if err != nil {
				return err
			}
			storeRoot := claimNextStoreRoot(cityPath)
			store, err := openWorkStoreAt(storeRoot, cityPath)
			if err != nil {
				return err
			}
			result, err := claimNextWorkFn(cmd.Context(), store, storeRoot, template, assignee, claimIdentityCandidates(
				assignee,
				os.Getenv("GC_SESSION_ID"),
				os.Getenv("GC_SESSION_NAME"),
				os.Getenv("GC_ALIAS"),
			))
			if err != nil {
				return err
			}
			if jsonOut {
				return writePreLaunchClaimResult(stdout, result)
			}
			if result.Bead.ID == "" {
				fmt.Fprintln(stdout, "no work") //nolint:errcheck
				return nil
			}
			fmt.Fprintf(stdout, "%s\n", result.Bead.ID) //nolint:errcheck
			_ = stderr
			return nil
		},
	}
	cmd.Flags().StringVar(&template, "template", "", "agent template/routing target")
	cmd.Flags().StringVar(&assignee, "assignee", "", "session identity to claim as")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit pre_launch JSON")
	return cmd
}

type claimNextResult struct {
	Bead   beads.Bead
	Reason string
}

func claimNextWork(ctx context.Context, store beads.Store, claimDir, template, assignee string, identities []string) (claimNextResult, error) {
	identities = claimIdentityCandidates(append([]string{assignee}, identities...)...)
	for _, identity := range identities {
		existing, err := store.List(beads.ListQuery{Assignee: identity, Status: "in_progress", Limit: 1})
		if err != nil {
			return claimNextResult{}, err
		}
		if len(existing) > 0 {
			return claimNextResult{Bead: existing[0], Reason: "existing_assignment"}, nil
		}
	}

	ready, err := store.Ready()
	if err != nil {
		return claimNextResult{}, err
	}
	for _, candidate := range ready {
		if claimNextHasIdentity(candidate.Assignee, identities) {
			return claimNextResult{Bead: candidate, Reason: "ready_assignment"}, nil
		}
	}

	routeTargets := claimRouteTargets(template)
	for _, candidate := range ready {
		if strings.TrimSpace(candidate.Assignee) != "" || !claimNextMatchesRoute(candidate, routeTargets) {
			continue
		}
		claimed, ok, err := claimNextCandidate(ctx, store, claimDir, candidate.ID, assignee)
		if err != nil {
			return claimNextResult{}, err
		}
		if !ok {
			continue
		}
		return claimNextResult{Bead: claimed, Reason: "claimed"}, nil
	}

	for _, target := range routeTargets {
		molecules, err := store.List(beads.ListQuery{
			Metadata: map[string]string{"gc.routed_to": target},
			Status:   "open",
			Type:     "molecule",
			Limit:    10,
		})
		if err != nil {
			return claimNextResult{}, err
		}
		for _, candidate := range molecules {
			if strings.TrimSpace(candidate.Assignee) != "" {
				continue
			}
			claimed, ok, err := claimNextCandidate(ctx, store, claimDir, candidate.ID, assignee)
			if err != nil {
				return claimNextResult{}, err
			}
			if !ok {
				continue
			}
			return claimNextResult{Bead: claimed, Reason: "claimed_molecule"}, nil
		}
	}

	return claimNextResult{Reason: "no_work"}, nil
}

var claimWork = beads.ClaimWithBD

func claimNextStoreRoot(cityPath string) string {
	if root := strings.TrimSpace(os.Getenv("GC_STORE_ROOT")); root != "" {
		return root
	}
	return cityPath
}

func claimIdentityCandidates(values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, value := range values {
		add(value)
		if legacy := claimLegacyWorkflowControlName(value); legacy != "" {
			add(legacy)
		}
	}
	return out
}

func claimRouteTargets(template string) []string {
	return claimIdentityCandidates(template)
}

func claimLegacyWorkflowControlName(value string) string {
	value = strings.TrimSpace(value)
	const suffix = "control-dispatcher"
	if !strings.HasSuffix(value, suffix) {
		return ""
	}
	return strings.TrimSuffix(value, suffix) + "workflow-control"
}

func claimNextHasIdentity(assignee string, identities []string) bool {
	assignee = strings.TrimSpace(assignee)
	for _, identity := range identities {
		if assignee == identity {
			return true
		}
	}
	return false
}

func claimNextMatchesRoute(candidate beads.Bead, routeTargets []string) bool {
	routedTo := strings.TrimSpace(candidate.Metadata["gc.routed_to"])
	for _, target := range routeTargets {
		if routedTo == target {
			return true
		}
	}
	return false
}

func claimNextCandidate(ctx context.Context, store beads.Store, claimDir, beadID, assignee string) (beads.Bead, bool, error) {
	if err := claimWork(ctx, claimDir, beadID, assignee); err != nil {
		if beads.IsClaimConflict(err) {
			return beads.Bead{}, false, nil
		}
		return beads.Bead{}, false, err
	}
	claimed, err := store.Get(beadID)
	if err != nil {
		return beads.Bead{}, false, err
	}
	if claimed.Assignee != assignee {
		return beads.Bead{}, false, nil
	}
	return claimed, true, nil
}

func writePreLaunchClaimResult(w io.Writer, result claimNextResult) error {
	type response struct {
		Action      string            `json:"action"`
		Reason      string            `json:"reason,omitempty"`
		Env         map[string]string `json:"env,omitempty"`
		NudgeAppend string            `json:"nudge_append,omitempty"`
		Metadata    map[string]string `json:"metadata,omitempty"`
	}
	if result.Bead.ID == "" {
		return json.NewEncoder(w).Encode(response{Action: "drain", Reason: result.Reason})
	}
	return json.NewEncoder(w).Encode(response{
		Action:      "continue",
		Reason:      result.Reason,
		Env:         map[string]string{"GC_WORK_BEAD": result.Bead.ID},
		NudgeAppend: "\n\nClaimed work bead: " + result.Bead.ID,
		Metadata:    map[string]string{"pre_launch.user.claimed_work_bead": result.Bead.ID},
	})
}

func firstNonEmptyWorkEnv(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
