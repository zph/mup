package upgrade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zph/mup/pkg/topology"
)

// replicaSetProbe reads and changes live replica set state. The upgrade loop
// depends on this interface so its ordering and safety gates can be tested
// without a running cluster.
type replicaSetProbe interface {
	Status(ctx context.Context, rsName string, hosts []string) ([]ReplicaSetMember, error)
	StepDown(ctx context.Context, primaryHost, rsName string, hosts []string) (*FailoverEvent, error)
}

// driverReplicaSetProbe is the replicaSetProbe backed by the MongoDB driver.
type driverReplicaSetProbe struct{}

func (driverReplicaSetProbe) Status(ctx context.Context, rsName string, hosts []string) ([]ReplicaSetMember, error) {
	return GetReplicaSetStatus(ctx, rsName, hosts)
}

func (driverReplicaSetProbe) StepDown(ctx context.Context, primaryHost, rsName string, hosts []string) (*FailoverEvent, error) {
	return StepDownPrimary(ctx, primaryHost, rsName, hosts)
}

const (
	// defaultSettleTimeout bounds how long the upgrade waits for every member
	// to be healthy before it takes the next node down.
	defaultSettleTimeout = 10 * time.Minute
	defaultSettlePoll    = 2 * time.Second
)

// nodeChoice is the next node to upgrade and how to treat it.
type nodeChoice struct {
	index         int
	role          string
	needsStepDown bool
}

// nextNodeToUpgrade picks the next pending node from current replica set
// state: any non-primary first, the primary only when nothing else is left.
// It fails if a pending node is not a member, rather than guessing its role.
//
// Members are matched by port: mup is local-only, so every member runs on
// this machine and its port is unique, while the host a member was registered
// under (localhost, 127.0.0.1, a hostname) may differ from the topology.
//
// UPGSAFE-005
func nextNodeToUpgrade(pending []topology.MongodNode, members []ReplicaSetMember) (nodeChoice, error) {
	byPort := make(map[int]ReplicaSetMember, len(members))
	for _, m := range members {
		byPort[m.Port] = m
	}

	primaryIndex := -1
	for i, node := range pending {
		m, ok := byPort[node.Port]
		if !ok {
			return nodeChoice{}, fmt.Errorf("node %s:%d is not a member of the replica set; refusing to guess its role", node.Host, node.Port)
		}
		if m.IsPrimary {
			primaryIndex = i
			continue
		}
		return nodeChoice{index: i, role: m.StateStr}, nil
	}

	if primaryIndex == -1 {
		return nodeChoice{}, fmt.Errorf("no node left to upgrade")
	}
	return nodeChoice{
		index: primaryIndex,
		role:  memberStatePrimary.String(),
		// A single-member set has nobody to fail over to; it is just restarted.
		needsStepDown: len(members) > 1,
	}, nil
}

// validateFailover checks that a stepdown actually moved the primary.
// UPGSAFE-006
func validateFailover(event *FailoverEvent) error {
	if event == nil {
		return fmt.Errorf("stepdown returned no failover result")
	}
	if event.NewPrimary == "" {
		return fmt.Errorf("no new primary was elected after stepping down %s", event.OldPrimary)
	}
	if sameLocalNode(event.NewPrimary, event.OldPrimary) {
		return fmt.Errorf("stepdown did not move the primary: %s is still PRIMARY", event.OldPrimary)
	}
	return nil
}

// sameLocalNode reports whether two host:port addresses name the same local
// node. Only ports are compared: every node runs on this machine, and the
// replica set may know a member as 127.0.0.1 where the topology says localhost.
func sameLocalNode(a, b string) bool {
	portA, okA := addressPort(a)
	portB, okB := addressPort(b)
	return okA && okB && portA == portB
}

// addressPort returns the port of a host:port address.
func addressPort(address string) (string, bool) {
	idx := strings.LastIndex(address, ":")
	if idx < 0 || idx == len(address)-1 {
		return "", false
	}
	return address[idx+1:], true
}

// versionMatches reports whether a running server version satisfies the
// requested one. A requested "7.0" matches any 7.0.x; "7.0.5" matches the
// Percona build "7.0.5-4".
// UPGSAFE-007
func versionMatches(actual, expected string) bool {
	if actual == "" || expected == "" {
		return false
	}
	return actual == expected ||
		strings.HasPrefix(actual, expected+".") ||
		strings.HasPrefix(actual, expected+"-")
}

// replicaSetUpgrade upgrades the members of one replica set one at a time.
type replicaSetUpgrade struct {
	probe         replicaSetProbe
	rsName        string
	settleTimeout time.Duration
	pollInterval  time.Duration
	// maxLag is how far behind the primary a secondary may be; zero disables
	// the replication lag gate.
	maxLag time.Duration

	// isDone reports whether a node (host:port) needs no further work.
	isDone func(hostPort string) bool
	// upgradeNode upgrades one node. When needsStepDown is true the node is
	// the current primary and must be stepped down first.
	upgradeNode func(ctx context.Context, node topology.MongodNode, role string, needsStepDown bool) error
}

// execute upgrades every pending node. Before each node it waits for the
// whole set to be healthy and re-reads member roles, so a node is never taken
// down while another is still recovering, and a primary that moved during the
// upgrade is still handled last.
// UPGSAFE-005
func (r *replicaSetUpgrade) execute(ctx context.Context, nodes []topology.MongodNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes to upgrade")
	}

	allHosts := make([]string, 0, len(nodes))
	var pending []topology.MongodNode
	for _, node := range nodes {
		hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
		allHosts = append(allHosts, hostPort)
		if r.isDone(hostPort) {
			fmt.Printf("    %s already upgraded, skipping...\n", hostPort)
			continue
		}
		pending = append(pending, node)
	}

	for len(pending) > 0 {
		members, err := r.waitUntilSettled(ctx, allHosts)
		if err != nil {
			return err
		}

		choice, err := nextNodeToUpgrade(pending, members)
		if err != nil {
			return err
		}

		node := pending[choice.index]
		if err := r.upgradeNode(ctx, node, choice.role, choice.needsStepDown); err != nil {
			return err
		}
		pending = append(pending[:choice.index], pending[choice.index+1:]...)
	}

	// Leave the phase only once the last restarted node has rejoined.
	if _, err := r.waitUntilSettled(ctx, allHosts); err != nil {
		return err
	}
	return nil
}

// waitUntilSettled polls until the replica set has a primary and every member
// is healthy, returning the members it saw.
func (r *replicaSetUpgrade) waitUntilSettled(ctx context.Context, hosts []string) ([]ReplicaSetMember, error) {
	deadline := time.Now().Add(r.settleTimeout)
	var lastErr error

	for {
		members, err := r.probe.Status(ctx, r.rsName, hosts)
		if err == nil {
			err = evaluateReplicaSetHealth(members)
		}
		// UPGSAFE-013
		if err == nil && r.maxLag > 0 {
			err = checkReplicationLag(members, r.maxLag)
		}
		if err == nil {
			return members, nil
		}
		lastErr = err

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("replica set %s did not become healthy within %s: %w", r.rsName, r.settleTimeout, lastErr)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for replica set %s to become healthy: %w", r.rsName, errors.Join(ctx.Err(), lastErr))
		case <-time.After(r.pollInterval):
		}
	}
}
