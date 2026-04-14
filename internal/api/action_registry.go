package api

import (
	"reflect"

	"github.com/gastownhall/gascity/internal/api/specgen"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/gastownhall/gascity/internal/workspacesvc"
)

// t is shorthand for getting reflect.Type from a zero value.
func t[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// BuildActionRegistry returns the canonical action registry with full
// request/response type mappings. This is the single source of truth
// for spec generation — every WS action maps to its Go types.
//
// To add a new action: add a case in handleSocketRequest AND a Register
// call here. TestAsyncAPIActionsMatchGoCode catches mismatches.
func BuildActionRegistry() *specgen.Registry {
	r := specgen.NewRegistry()

	// ── Health / Status ──────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "health.get", Description: "Health check"})
	r.Register(specgen.ActionDef{Action: "status.get", Description: "City status snapshot"})

	// ── City ─────────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "cities.list", Description: "List managed cities (supervisor)",
		ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "city.get", Description: "Get city details"})
	r.Register(specgen.ActionDef{Action: "city.patch", Description: "Update city (suspend/resume)", IsMutation: true,
		RequestType: t[cityPatchRequest]()})

	// ── Config ───────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "config.get", Description: "Get parsed city configuration",
		ResponseType: t[configResponse]()})
	r.Register(specgen.ActionDef{Action: "config.explain", Description: "Explain config resolution"})
	r.Register(specgen.ActionDef{Action: "config.validate", Description: "Validate city configuration"})

	// ── Sessions ─────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "sessions.list", Description: "List sessions",
		RequestType: t[socketSessionsListPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "session.get", Description: "Get session details",
		RequestType: t[socketSessionTargetPayload](), ResponseType: t[sessionResponse]()})
	r.Register(specgen.ActionDef{Action: "session.create", Description: "Create a new session", IsMutation: true,
		RequestType: t[sessionCreateRequest]()})
	r.Register(specgen.ActionDef{Action: "session.suspend", Description: "Suspend a session", IsMutation: true,
		RequestType: t[socketSessionTargetPayload]()})
	r.Register(specgen.ActionDef{Action: "session.close", Description: "Close a session", IsMutation: true,
		RequestType: t[socketSessionTargetPayload]()})
	r.Register(specgen.ActionDef{Action: "session.stop", Description: "Stop a session", IsMutation: true,
		RequestType: t[socketSessionTargetPayload]()})
	r.Register(specgen.ActionDef{Action: "session.wake", Description: "Wake a suspended session", IsMutation: true,
		RequestType: t[socketSessionTargetPayload]()})
	r.Register(specgen.ActionDef{Action: "session.rename", Description: "Rename a session", IsMutation: true,
		RequestType: t[socketSessionRenamePayload]()})
	r.Register(specgen.ActionDef{Action: "session.respond", Description: "Respond to a session prompt", IsMutation: true,
		RequestType: t[socketSessionRespondPayload]()})
	r.Register(specgen.ActionDef{Action: "session.kill", Description: "Force-kill a session", IsMutation: true,
		RequestType: t[socketSessionTargetPayload]()})
	r.Register(specgen.ActionDef{Action: "session.pending", Description: "Get pending input requests",
		RequestType: t[socketSessionTargetPayload]()})
	r.Register(specgen.ActionDef{Action: "session.submit", Description: "Submit work to a session", IsMutation: true,
		RequestType: t[socketSessionSubmitPayload]()})
	r.Register(specgen.ActionDef{Action: "session.transcript", Description: "Get session transcript",
		RequestType: t[socketSessionTranscriptPayload]()})
	r.Register(specgen.ActionDef{Action: "session.patch", Description: "Update session metadata", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "session.messages", Description: "Get session messages"})
	r.Register(specgen.ActionDef{Action: "session.agents.list", Description: "List agents in a session"})
	r.Register(specgen.ActionDef{Action: "session.agent.get", Description: "Get session agent details"})

	// ── Beads ────────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "beads.list", Description: "List beads with filters",
		RequestType: t[socketBeadsListPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "beads.ready", Description: "List ready beads",
		ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "beads.graph", Description: "Get bead dependency graph",
		RequestType: t[socketBeadGraphPayload]()})
	r.Register(specgen.ActionDef{Action: "bead.get", Description: "Get a bead by ID",
		RequestType: t[socketIDPayload](), ResponseType: t[beads.Bead]()})
	r.Register(specgen.ActionDef{Action: "bead.deps", Description: "Get bead dependencies",
		RequestType: t[socketIDPayload]()})
	r.Register(specgen.ActionDef{Action: "bead.create", Description: "Create a new bead", IsMutation: true,
		RequestType: t[beadCreateRequest](), ResponseType: t[beads.Bead]()})
	r.Register(specgen.ActionDef{Action: "bead.close", Description: "Close a bead", IsMutation: true,
		RequestType: t[socketIDPayload](), ResponseType: t[beads.Bead]()})
	r.Register(specgen.ActionDef{Action: "bead.update", Description: "Update a bead", IsMutation: true,
		RequestType: t[socketIDPayload](), ResponseType: t[beads.Bead]()})
	r.Register(specgen.ActionDef{Action: "bead.reopen", Description: "Reopen a closed bead", IsMutation: true,
		RequestType: t[socketIDPayload](), ResponseType: t[beads.Bead]()})
	r.Register(specgen.ActionDef{Action: "bead.assign", Description: "Assign a bead to an agent", IsMutation: true,
		RequestType: t[socketBeadAssignPayload](), ResponseType: t[beads.Bead]()})
	r.Register(specgen.ActionDef{Action: "bead.delete", Description: "Delete a bead", IsMutation: true,
		RequestType: t[socketIDPayload]()})

	// ── Mail ─────────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "mail.list", Description: "List mail messages",
		RequestType: t[socketMailListPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "mail.get", Description: "Get a mail message",
		RequestType: t[socketMailGetPayload](), ResponseType: t[mail.Message]()})
	r.Register(specgen.ActionDef{Action: "mail.count", Description: "Count mail messages",
		RequestType: t[socketMailCountPayload]()})
	r.Register(specgen.ActionDef{Action: "mail.thread", Description: "Get a mail thread",
		RequestType: t[socketMailThreadPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "mail.read", Description: "Mark mail as read", IsMutation: true,
		RequestType: t[socketMailGetPayload](), ResponseType: t[mail.Message]()})
	r.Register(specgen.ActionDef{Action: "mail.mark_unread", Description: "Mark mail as unread", IsMutation: true,
		RequestType: t[socketMailGetPayload](), ResponseType: t[mail.Message]()})
	r.Register(specgen.ActionDef{Action: "mail.archive", Description: "Archive a mail message", IsMutation: true,
		RequestType: t[socketMailGetPayload](), ResponseType: t[mail.Message]()})
	r.Register(specgen.ActionDef{Action: "mail.reply", Description: "Reply to a mail message", IsMutation: true,
		RequestType: t[mailReplyRequest]()})
	r.Register(specgen.ActionDef{Action: "mail.send", Description: "Send a new mail message", IsMutation: true,
		RequestType: t[mailSendRequest](), ResponseType: t[mail.Message]()})
	r.Register(specgen.ActionDef{Action: "mail.delete", Description: "Delete a mail message", IsMutation: true,
		RequestType: t[socketMailGetPayload]()})

	// ── Events ───────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "events.list", Description: "List events with filters",
		RequestType: t[socketEventsListPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "event.emit", Description: "Emit a custom event", IsMutation: true,
		RequestType: t[events.Event]()})

	// ── Agents ───────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "agents.list", Description: "List agents",
		RequestType: t[socketAgentsListPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "agent.get", Description: "Get agent details",
		RequestType: t[socketNamePayload](), ResponseType: t[agentResponse]()})
	r.Register(specgen.ActionDef{Action: "agent.suspend", Description: "Suspend an agent", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "agent.resume", Description: "Resume a suspended agent", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "agent.create", Description: "Create an agent", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "agent.update", Description: "Update agent config", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "agent.delete", Description: "Delete an agent", IsMutation: true,
		RequestType: t[socketNamePayload]()})

	// ── Rigs ─────────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "rigs.list", Description: "List rigs",
		ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "rig.get", Description: "Get rig details",
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "rig.suspend", Description: "Suspend a rig", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "rig.resume", Description: "Resume a suspended rig", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "rig.restart", Description: "Restart a rig", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "rig.create", Description: "Create a rig", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "rig.update", Description: "Update rig config", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "rig.delete", Description: "Delete a rig", IsMutation: true,
		RequestType: t[socketNamePayload]()})

	// ── Convoys ──────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "convoys.list", Description: "List convoys",
		ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "convoy.get", Description: "Get convoy details",
		RequestType: t[socketIDPayload]()})
	r.Register(specgen.ActionDef{Action: "convoy.create", Description: "Create a convoy", IsMutation: true,
		RequestType: t[convoyCreateRequest]()})
	r.Register(specgen.ActionDef{Action: "convoy.add", Description: "Add items to a convoy", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "convoy.remove", Description: "Remove items from a convoy", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "convoy.check", Description: "Check convoy status"})
	r.Register(specgen.ActionDef{Action: "convoy.close", Description: "Close a convoy", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "convoy.delete", Description: "Delete a convoy", IsMutation: true})

	// ── Services ─────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "services.list", Description: "List workspace services",
		ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "service.get", Description: "Get service status",
		RequestType: t[socketNamePayload](), ResponseType: t[workspacesvc.Status]()})
	r.Register(specgen.ActionDef{Action: "service.restart", Description: "Restart a service", IsMutation: true,
		RequestType: t[socketNamePayload]()})

	// ── Providers ────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "providers.list", Description: "List AI providers",
		RequestType: t[socketProvidersListPayload](), ResponseType: t[listResponse]()})
	r.Register(specgen.ActionDef{Action: "provider.get", Description: "Get provider details",
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "provider.create", Description: "Create a provider", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "provider.update", Description: "Update provider config", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "provider.delete", Description: "Delete a provider", IsMutation: true,
		RequestType: t[socketNamePayload]()})

	// ── Formulas ─────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "formulas.list", Description: "List formulas"})
	r.Register(specgen.ActionDef{Action: "formulas.feed", Description: "Formula activity feed"})
	r.Register(specgen.ActionDef{Action: "formula.get", Description: "Get formula details"})
	r.Register(specgen.ActionDef{Action: "formula.runs", Description: "Get formula run history"})

	// ── Orders ───────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "orders.list", Description: "List orders"})
	r.Register(specgen.ActionDef{Action: "orders.check", Description: "Check order gate conditions"})
	r.Register(specgen.ActionDef{Action: "orders.history", Description: "Get order history"})
	r.Register(specgen.ActionDef{Action: "orders.feed", Description: "Order activity feed"})
	r.Register(specgen.ActionDef{Action: "order.get", Description: "Get order details",
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "order.enable", Description: "Enable an order", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "order.disable", Description: "Disable an order", IsMutation: true,
		RequestType: t[socketNamePayload]()})
	r.Register(specgen.ActionDef{Action: "order.history.detail", Description: "Get order history detail",
		RequestType: t[socketIDPayload](), ResponseType: t[beads.Bead]()})

	// ── Packs ────────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "packs.list", Description: "List installed packs"})

	// ── Sling (dispatch) ─────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "sling.run", Description: "Run sling dispatch", IsMutation: true,
		RequestType: t[slingBody]()})

	// ── External messaging ───────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "extmsg.inbound", Description: "Process inbound external message", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.outbound", Description: "Send outbound external message", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.bindings.list", Description: "List external message bindings"})
	r.Register(specgen.ActionDef{Action: "extmsg.bind", Description: "Bind external messaging channel", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.unbind", Description: "Unbind external messaging channel", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.groups.lookup", Description: "Look up messaging groups"})
	r.Register(specgen.ActionDef{Action: "extmsg.groups.ensure", Description: "Ensure messaging group exists", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.participant.upsert", Description: "Add/update messaging participant", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.participant.remove", Description: "Remove messaging participant", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.transcript.list", Description: "List external message transcript"})
	r.Register(specgen.ActionDef{Action: "extmsg.transcript.ack", Description: "Acknowledge transcript messages", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.adapters.list", Description: "List messaging adapters"})
	r.Register(specgen.ActionDef{Action: "extmsg.adapters.register", Description: "Register messaging adapter", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "extmsg.adapters.unregister", Description: "Unregister messaging adapter", IsMutation: true})

	// ── Patches (config overrides) ───────────────────────────────
	r.Register(specgen.ActionDef{Action: "patches.agents.list", Description: "List agent patches"})
	r.Register(specgen.ActionDef{Action: "patches.agent.get", Description: "Get agent patch"})
	r.Register(specgen.ActionDef{Action: "patches.agents.set", Description: "Set agent patch", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "patches.agent.delete", Description: "Delete agent patch", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "patches.rigs.list", Description: "List rig patches"})
	r.Register(specgen.ActionDef{Action: "patches.rig.get", Description: "Get rig patch"})
	r.Register(specgen.ActionDef{Action: "patches.rigs.set", Description: "Set rig patch", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "patches.rig.delete", Description: "Delete rig patch", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "patches.providers.list", Description: "List provider patches"})
	r.Register(specgen.ActionDef{Action: "patches.provider.get", Description: "Get provider patch"})
	r.Register(specgen.ActionDef{Action: "patches.providers.set", Description: "Set provider patch", IsMutation: true})
	r.Register(specgen.ActionDef{Action: "patches.provider.delete", Description: "Delete provider patch", IsMutation: true})

	// ── Workflows ────────────────────────────────────────────────
	r.Register(specgen.ActionDef{Action: "workflow.get", Description: "Get workflow details"})
	r.Register(specgen.ActionDef{Action: "workflow.delete", Description: "Delete a workflow", IsMutation: true})

	// ── Subscriptions (protocol-level) ───────────────────────────
	r.Register(specgen.ActionDef{Action: "subscription.start", Description: "Start an event or session stream subscription"})
	r.Register(specgen.ActionDef{Action: "subscription.stop", Description: "Stop a subscription"})

	return r
}
