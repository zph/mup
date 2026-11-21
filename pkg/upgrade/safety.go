package upgrade

import (
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// memberState is a MongoDB replica set member state as reported by
// replSetGetStatus.
type memberState int32

const (
	memberStateStartup    memberState = 0
	memberStatePrimary    memberState = 1
	memberStateSecondary  memberState = 2
	memberStateRecovering memberState = 3
	memberStateStartup2   memberState = 5
	memberStateUnknown    memberState = 6
	memberStateArbiter    memberState = 7
	memberStateDown       memberState = 8
	memberStateRollback   memberState = 9
	memberStateRemoved    memberState = 10
)

var memberStateNames = map[memberState]string{
	memberStateStartup:    "STARTUP",
	memberStatePrimary:    "PRIMARY",
	memberStateSecondary:  "SECONDARY",
	memberStateRecovering: "RECOVERING",
	memberStateStartup2:   "STARTUP2",
	memberStateUnknown:    "UNKNOWN",
	memberStateArbiter:    "ARBITER",
	memberStateDown:       "DOWN",
	memberStateRollback:   "ROLLBACK",
	memberStateRemoved:    "REMOVED",
}

func (s memberState) String() string {
	if name, ok := memberStateNames[s]; ok {
		return name
	}
	return memberStateNames[memberStateUnknown]
}

// isSettled reports whether a member is in a steady, serving state.
func (s memberState) isSettled() bool {
	return s == memberStatePrimary || s == memberStateSecondary || s == memberStateArbiter
}

const memberHealthUp int32 = 1

// Address returns the member's host:port.
func (m ReplicaSetMember) Address() string {
	return fmt.Sprintf("%s:%d", m.Host, m.Port)
}

// isHealthy reports whether a member is reachable and in a steady state.
func (m ReplicaSetMember) isHealthy() bool {
	return m.Health == memberHealthUp && memberState(m.StateNum).isSettled()
}

// parseReplicaSetMembers extracts members from a replSetGetStatus reply.
func parseReplicaSetMembers(status bson.M) ([]ReplicaSetMember, error) {
	rawMembers, ok := status["members"].(bson.A)
	if !ok {
		return nil, fmt.Errorf("unexpected replica set status format")
	}

	members := make([]ReplicaSetMember, 0, len(rawMembers))
	for _, raw := range rawMembers {
		doc, ok := raw.(bson.M)
		if !ok {
			continue
		}

		name, _ := doc["name"].(string)
		stateStr, _ := doc["stateStr"].(string)
		state := int32Field(doc["state"])

		var host string
		var port int
		if idx := strings.LastIndex(name, ":"); idx >= 0 {
			host = name[:idx]
			if _, err := fmt.Sscanf(name[idx+1:], "%d", &port); err != nil {
				return nil, fmt.Errorf("unexpected member name %q: %w", name, err)
			}
		}

		members = append(members, ReplicaSetMember{
			Host:      host,
			Port:      port,
			StateStr:  stateStr,
			StateNum:  state,
			Health:    int32Field(doc["health"]),
			IsPrimary: memberState(state) == memberStatePrimary,
		})
	}
	return members, nil
}

// int32Field reads a numeric BSON field that servers report as int32, int64
// or double depending on version.
func int32Field(raw interface{}) int32 {
	switch v := raw.(type) {
	case int32:
		return v
	case int64:
		return int32(v)
	case float64:
		return int32(v)
	}
	return 0
}

// evaluateReplicaSetHealth checks that a replica set has a primary and that
// every member is reachable and settled. Arbiters count as healthy.
// UPGSAFE-001
func evaluateReplicaSetHealth(members []ReplicaSetMember) error {
	if len(members) == 0 {
		return fmt.Errorf("replica set reports no members")
	}

	var hasPrimary bool
	var unhealthy []string
	for _, m := range members {
		if m.IsPrimary {
			hasPrimary = true
		}
		if !m.isHealthy() {
			unhealthy = append(unhealthy, fmt.Sprintf("%s (%s)", m.Address(), memberState(m.StateNum)))
		}
	}

	if !hasPrimary {
		return fmt.Errorf("no primary found in replica set")
	}
	if len(unhealthy) > 0 {
		return fmt.Errorf("not all members are healthy: %s", strings.Join(unhealthy, ", "))
	}
	return nil
}

// criticalAction is what to do after a critical-operation prompt.
type criticalAction int

const (
	// criticalAbort is the zero value so an unrecognised answer never proceeds.
	criticalAbort criticalAction = iota
	criticalProceed
	criticalPause
)

// criticalPromptAction maps a prompt answer to an action. Only an explicit
// "continue" proceeds with an irreversible operation.
// UPGSAFE-003
func criticalPromptAction(response PromptResponse) criticalAction {
	switch response {
	case PromptResponseContinue:
		return criticalProceed
	case PromptResponsePause:
		return criticalPause
	}
	return criticalAbort
}

// ensureNoSkippedNodes fails when any node was skipped, because finishing the
// upgrade (FCV change, old supervisor shutdown, metadata update) assumes
// every node runs the new version.
// UPGSAFE-004
func ensureNoSkippedNodes(state *UpgradeState) error {
	state.mu.RLock()
	defer state.mu.RUnlock()
	if len(state.SkippedNodes) == 0 {
		return nil
	}
	return fmt.Errorf("upgrade is incomplete: %d node(s) were skipped and still run the old version: %s",
		len(state.SkippedNodes), strings.Join(state.SkippedNodes, ", "))
}
