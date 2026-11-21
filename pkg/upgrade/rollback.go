package upgrade

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/zph/mup/pkg/naming"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/topology"
)

// nodeMover moves a node between the new version's supervisor and the old one.
type nodeMover interface {
	// StopOnNew stops the node under the new version's supervisor. It succeeds
	// if the node is not running there.
	StopOnNew(ctx context.Context, program string) error
	// StartOnOld starts the node under the old version's supervisor and waits
	// for it to be running.
	StartOnOld(ctx context.Context, program string) error
	// VerifyVersion checks that the running node reports the given version.
	VerifyVersion(ctx context.Context, program, version string) error
}

// canRollBack reports whether an upgrade can be undone by moving nodes back to
// the old binaries. That is only safe before post-upgrade tasks begin: after
// that the FCV may have been raised and the old supervisor shut down, and
// going back would be a downgrade.
// UPGSAFE-014
func canRollBack(state *UpgradeState) error {
	state.mu.RLock()
	defer state.mu.RUnlock()

	switch state.OverallStatus {
	case OverallStatusFailed, OverallStatusPaused, OverallStatusInProgress:
	default:
		return fmt.Errorf("cannot roll back: upgrade status is %s", state.OverallStatus)
	}

	if phase, ok := state.Phases[PhasePostUpgrade]; ok && phase.Status != PhaseStatusNotStarted {
		return fmt.Errorf("cannot roll back: post-upgrade tasks have started (%s), so the FCV or active version may already have changed; resume the upgrade instead", phase.Status)
	}
	return nil
}

// needsRollback reports whether the upgrade started or finished work on a node.
// UPGSAFE-014
func needsRollback(state *UpgradeState, hostPort string) bool {
	state.mu.RLock()
	defer state.mu.RUnlock()

	node, ok := state.Nodes[hostPort]
	if !ok {
		return false
	}
	switch node.Status {
	case NodeStatusCompleted, NodeStatusInProgress, NodeStatusFailed:
		return true
	}
	return false
}

// rollbackNode moves one node back to the old binaries. The old binary is
// only started once the new one is confirmed stopped, since both use the same
// data directory.
// UPGSAFE-014
func rollbackNode(ctx context.Context, mover nodeMover, program, fromVersion string) error {
	if err := mover.StopOnNew(ctx, program); err != nil {
		return fmt.Errorf("failed to stop %s on the new version: %w", program, err)
	}
	if err := mover.StartOnOld(ctx, program); err != nil {
		return fmt.Errorf("failed to start %s on the old version: %w", program, err)
	}
	if err := mover.VerifyVersion(ctx, program, fromVersion); err != nil {
		return fmt.Errorf("%s did not come back on %s: %w", program, fromVersion, err)
	}
	return nil
}

// replicaSetRollback moves the upgraded members of one replica set back to
// the old binaries.
type replicaSetRollback struct {
	probe         replicaSetProbe
	mover         nodeMover
	state         *UpgradeState
	rsName        string
	fromVersion   string
	programPrefix string // "mongod" or "config"
	settleTimeout time.Duration
	pollInterval  time.Duration
	save          func() error
	// afterNode is called after a node has been moved back; tests use it.
	afterNode func(node topology.MongodNode)
}

// execute rolls back every member the upgrade touched. Members that are not
// healthy (typically the node whose upgrade failed) go first, because the set
// cannot become healthy until they are back. The rest follow the same gated,
// primary-last order as an upgrade.
// UPGSAFE-014
func (r *replicaSetRollback) execute(ctx context.Context, nodes []topology.MongodNode) error {
	hosts := make([]string, 0, len(nodes))
	var touched []topology.MongodNode
	for _, node := range nodes {
		hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
		hosts = append(hosts, hostPort)
		if needsRollback(r.state, hostPort) {
			touched = append(touched, node)
		}
	}
	if len(touched) == 0 {
		return nil
	}

	// If the set cannot be read at all, every touched node counts as unhealthy.
	healthy := make(map[int]bool)
	if members, err := r.probe.Status(ctx, r.rsName, hosts); err == nil {
		for _, m := range members {
			if m.isHealthy() {
				healthy[m.Port] = true
			}
		}
	}
	for _, node := range touched {
		if healthy[node.Port] {
			continue
		}
		if err := r.moveBack(ctx, node); err != nil {
			return err
		}
	}

	loop := &replicaSetUpgrade{
		probe:         r.probe,
		rsName:        r.rsName,
		settleTimeout: r.settleTimeout,
		pollInterval:  r.pollInterval,
		isDone: func(hostPort string) bool {
			return !needsRollback(r.state, hostPort)
		},
		upgradeNode: func(ctx context.Context, node topology.MongodNode, _ string, needsStepDown bool) error {
			if needsStepDown {
				hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
				event, err := r.probe.StepDown(ctx, hostPort, r.rsName, hosts)
				if err != nil {
					return fmt.Errorf("failed to step down primary %s: %w", hostPort, err)
				}
				if err := validateFailover(event); err != nil {
					return err
				}
			}
			return r.moveBack(ctx, node)
		},
	}
	return loop.execute(ctx, nodes)
}

func (r *replicaSetRollback) moveBack(ctx context.Context, node topology.MongodNode) error {
	hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
	program := naming.GetProgramName(r.programPrefix, node.Port)

	fmt.Printf("  Rolling back %s to %s...\n", hostPort, r.fromVersion)
	if err := rollbackNode(ctx, r.mover, program, r.fromVersion); err != nil {
		return err
	}

	r.state.UpdateNodeState(hostPort, NodeStatusRolledBack, "")
	if err := r.save(); err != nil {
		return fmt.Errorf("failed to record rollback of %s: %w", hostPort, err)
	}
	fmt.Printf("  ✓ %s is back on %s\n", hostPort, r.fromVersion)

	if r.afterNode != nil {
		r.afterNode(node)
	}
	return nil
}

const (
	rollbackStartTimeout = 2 * time.Minute
	rollbackStartPoll    = time.Second
)

// localNodeMover moves nodes between the two supervisors of a local upgrade.
type localNodeMover struct {
	oldSupervisor *supervisor.Manager
	newSupervisor *supervisor.Manager // nil when the new supervisor never started
}

func (m *localNodeMover) StopOnNew(_ context.Context, program string) error {
	if m.newSupervisor == nil || !m.newSupervisor.IsRunning() {
		return nil
	}
	status, err := m.newSupervisor.GetProcessStatus(program)
	if err != nil {
		// The new supervisor does not know the program, so it is not running there.
		return nil //nolint:nilerr // an unknown program needs no stop
	}
	if state := status.ProcessState(); state == supervisor.StateStopped || state.IsFailed() {
		return nil
	}
	return m.newSupervisor.StopProcess(program)
}

func (m *localNodeMover) StartOnOld(ctx context.Context, program string) error {
	if !m.oldSupervisor.IsRunning() {
		if err := m.oldSupervisor.Start(ctx); err != nil {
			return fmt.Errorf("failed to start the old supervisor: %w", err)
		}
	}
	if err := m.oldSupervisor.StartProcess(program); err != nil {
		return err
	}

	deadline := time.Now().Add(rollbackStartTimeout)
	for {
		status, err := m.oldSupervisor.GetProcessStatus(program)
		if err == nil {
			state := status.ProcessState()
			if state.IsRunning() {
				return nil
			}
			if state == supervisor.StateFatal {
				return fmt.Errorf("%s is FATAL under the old supervisor", program)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not reach RUNNING within %s", program, rollbackStartTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rollbackStartPoll):
		}
	}
}

func (m *localNodeMover) VerifyVersion(ctx context.Context, program, version string) error {
	port, err := portFromNodeID(program)
	if err != nil {
		return err
	}
	return waitForServerVersion(ctx, driverVersionLookup(port), version, versionVerifyTimeout, versionVerifyPoll)
}

// Rollback undoes an unfinished upgrade by moving every node it touched back
// to the old binaries, in the reverse of upgrade order: mongos, then shards,
// then config servers.
// UPGSAFE-014
func (lu *LocalUpgrader) Rollback(ctx context.Context) error {
	if err := canRollBack(lu.state); err != nil {
		return err
	}

	fmt.Println("Rolling Back MongoDB Cluster Upgrade")
	fmt.Println("====================================")
	fmt.Printf("Cluster: %s\n", lu.config.ClusterName)
	fmt.Printf("Returning to: %s\n\n", lu.config.FromVersion)

	newVersionDir := filepath.Join(lu.config.MetaDir, fmt.Sprintf("v%s", lu.config.ToVersion))
	mover := &localNodeMover{oldSupervisor: lu.supervisorMgr}
	// The new supervisor may never have been set up if the upgrade failed early.
	if newSupervisor, err := supervisor.LoadManager(newVersionDir, lu.config.ClusterName); err == nil {
		mover.newSupervisor = newSupervisor
	}
	save := func() error { return lu.config.StateManager.SaveState(lu.state) }

	for _, node := range lu.config.Topology.Mongos {
		hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
		if !needsRollback(lu.state, hostPort) {
			continue
		}
		fmt.Printf("  Rolling back mongos %s...\n", hostPort)
		if err := rollbackNode(ctx, mover, naming.GetProgramName("mongos", node.Port), lu.config.FromVersion); err != nil {
			return err
		}
		lu.state.UpdateNodeState(hostPort, NodeStatusRolledBack, "")
		if err := save(); err != nil {
			return err
		}
	}

	shards := make(map[string][]topology.MongodNode)
	for _, node := range lu.config.Topology.Mongod {
		shards[node.ReplicaSet] = append(shards[node.ReplicaSet], node)
	}
	shardNames := make([]string, 0, len(shards))
	for name := range shards {
		shardNames = append(shardNames, name)
	}
	sort.Strings(shardNames)

	rollbackSet := func(rsName, programPrefix string, nodes []topology.MongodNode) error {
		run := &replicaSetRollback{
			probe:         lu.replicaSetProbe(),
			mover:         mover,
			state:         lu.state,
			rsName:        rsName,
			fromVersion:   lu.config.FromVersion,
			programPrefix: programPrefix,
			settleTimeout: defaultSettleTimeout,
			pollInterval:  defaultSettlePoll,
			save:          save,
		}
		return run.execute(ctx, nodes)
	}

	for _, name := range shardNames {
		nodes := shards[name]
		if name == "" {
			// Standalone nodes have no replica set to coordinate with.
			for _, node := range nodes {
				hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
				if !needsRollback(lu.state, hostPort) {
					continue
				}
				if err := rollbackNode(ctx, mover, naming.GetProgramName("mongod", node.Port), lu.config.FromVersion); err != nil {
					return err
				}
				lu.state.UpdateNodeState(hostPort, NodeStatusRolledBack, "")
				if err := save(); err != nil {
					return err
				}
			}
			continue
		}
		if err := rollbackSet(name, "mongod", nodes); err != nil {
			return fmt.Errorf("rollback of replica set %s failed: %w", name, err)
		}
	}

	if configNodes := convertConfigToMongod(lu.config.Topology.ConfigSvr); len(configNodes) > 0 {
		if err := rollbackSet(configNodes[0].ReplicaSet, "config", configNodes); err != nil {
			return fmt.Errorf("rollback of config servers failed: %w", err)
		}
	}

	if lu.config.Topology.GetTopologyType() == "sharded" {
		if err := lu.RestoreBalancer(ctx); err != nil {
			return err
		}
	}

	if mover.newSupervisor != nil && mover.newSupervisor.IsRunning() {
		fmt.Print("  Stopping the new version's supervisor... ")
		if err := mover.newSupervisor.Stop(ctx); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("failed to stop the new supervisor: %w", err)
		}
		fmt.Println("✓")
	}
	lu.cleanupNextSymlink()

	lu.state.mu.Lock()
	lu.state.OverallStatus = OverallStatusRolledBack
	lu.state.mu.Unlock()
	if err := lu.config.StateManager.ArchiveState(lu.state); err != nil {
		return fmt.Errorf("failed to archive upgrade state: %w", err)
	}

	fmt.Printf("\n✓ Rollback complete: cluster %s is back on %s\n", lu.config.ClusterName, lu.config.FromVersion)
	return nil
}
