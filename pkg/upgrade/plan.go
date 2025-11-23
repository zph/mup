package upgrade

import (
	"context"
	"fmt"
	"strings"

	"github.com/zph/mup/pkg/topology"
)

// UpgradePlan represents a detailed plan of what will be upgraded and in what order
type UpgradePlan struct {
	ClusterName   string
	FromVersion   string
	ToVersion     string
	TopologyType  string
	Phases        []PlanPhase
	Prerequisites []string
	Warnings      []string
}

// PlanPhase represents a phase in the upgrade plan
type PlanPhase struct {
	Phase       int
	Name        string
	Description string
	Steps       []PlanStep
}

// PlanStep represents a single step in the upgrade plan
type PlanStep struct {
	Step        int
	Action      string
	Target      string
	Description string
	Critical    bool
}

// GenerateUpgradePlan generates a detailed upgrade plan for dry-run mode
func (lu *LocalUpgrader) GenerateUpgradePlan(ctx context.Context) (*UpgradePlan, error) {
	plan := &UpgradePlan{
		ClusterName:  lu.config.ClusterName,
		FromVersion:  lu.config.FromVersion,
		ToVersion:    lu.config.ToVersion,
		TopologyType: lu.config.Topology.GetTopologyType(),
		Phases:       []PlanPhase{},
		Prerequisites: []string{
			"All cluster processes must be running",
			"Supervisord must be running",
			fmt.Sprintf("Target version %s binaries will be downloaded", lu.config.ToVersion),
			"Sufficient disk space must be available",
			"Cluster must be healthy with no replication lag > 10s",
		},
	}

	// Add warnings
	if lu.config.UpgradeFCV {
		plan.Warnings = append(plan.Warnings, "FCV will be upgraded after binary upgrade - this is irreversible")
	}
	if !lu.config.ParallelShards {
		plan.Warnings = append(plan.Warnings, "Shards will be upgraded sequentially - use --parallel-shards for faster upgrades")
	}

	// Phase 0: Pre-flight validation
	phase0 := PlanPhase{
		Phase:       0,
		Name:        "Pre-Flight Validation",
		Description: "Validate prerequisites and cluster health",
		Steps: []PlanStep{
			{Step: 1, Action: "validate", Target: "upgrade path", Description: fmt.Sprintf("Validate %s → %s is allowed", plan.FromVersion, plan.ToVersion)},
			{Step: 2, Action: "check", Target: "supervisord", Description: "Verify supervisord is running"},
			{Step: 3, Action: "check", Target: "processes", Description: "Verify all MongoDB processes are running"},
			{Step: 4, Action: "download", Target: "binaries", Description: fmt.Sprintf("Download and verify %s binaries", plan.ToVersion)},
			{Step: 5, Action: "check", Target: "disk space", Description: "Verify sufficient disk space"},
			{Step: 6, Action: "connect", Target: "cluster", Description: "Connect to MongoDB cluster"},
			{Step: 7, Action: "check", Target: "FCV", Description: "Verify Feature Compatibility Version"},
			{Step: 8, Action: "check", Target: "health", Description: "Verify cluster health"},
			{Step: 9, Action: "check", Target: "replication", Description: "Verify replication lag"},
		},
	}
	plan.Phases = append(plan.Phases, phase0)

	stepNum := 1

	// Generate phases based on topology type
	switch plan.TopologyType {
	case "sharded":
		plan.Phases = append(plan.Phases, lu.generateShardedClusterPlan(&stepNum)...)
	case "replica_set":
		plan.Phases = append(plan.Phases, lu.generateReplicaSetPlan(&stepNum)...)
	case "standalone":
		plan.Phases = append(plan.Phases, lu.generateStandalonePlan(&stepNum)...)
	}

	// Final phase: FCV upgrade (if requested)
	if lu.config.UpgradeFCV {
		fcvPhase := PlanPhase{
			Phase:       len(plan.Phases) + 1,
			Name:        "Feature Compatibility Version Upgrade",
			Description: "Upgrade FCV to match new binary version",
			Steps: []PlanStep{
				{
					Step:        stepNum,
					Action:      "upgrade-fcv",
					Target:      "cluster",
					Description: fmt.Sprintf("Set FCV to %s", lu.config.ToVersion),
					Critical:    true,
				},
			},
		}
		plan.Phases = append(plan.Phases, fcvPhase)
	}

	return plan, nil
}

// generateShardedClusterPlan generates plan for sharded cluster
func (lu *LocalUpgrader) generateShardedClusterPlan(stepNum *int) []PlanPhase {
	phases := []PlanPhase{}

	// Phase 1: Config servers
	configPhase := PlanPhase{
		Phase:       1,
		Name:        "Upgrade Config Servers",
		Description: "Upgrade config server replica set",
		Steps:       []PlanStep{},
	}

	for _, node := range lu.config.Topology.ConfigSvr {
		configPhase.Steps = append(configPhase.Steps, PlanStep{
			Step:        *stepNum,
			Action:      "upgrade-node",
			Target:      fmt.Sprintf("%s:%d", node.Host, node.Port),
			Description: fmt.Sprintf("Stop, replace binary, start config server %s:%d", node.Host, node.Port),
		})
		*stepNum++
	}
	phases = append(phases, configPhase)

	// Phase 2: Shards
	shardNames := lu.getShardNames()
	for i, shardName := range shardNames {
		shardPhase := PlanPhase{
			Phase:       2 + i,
			Name:        fmt.Sprintf("Upgrade Shard: %s", shardName),
			Description: fmt.Sprintf("Upgrade shard replica set %s", shardName),
			Steps:       []PlanStep{},
		}

		nodes := lu.getNodesForShard(shardName)
		for _, node := range nodes {
			shardPhase.Steps = append(shardPhase.Steps, PlanStep{
				Step:        *stepNum,
				Action:      "upgrade-node",
				Target:      fmt.Sprintf("%s:%d", node.Host, node.Port),
				Description: fmt.Sprintf("Stop, replace binary, start shard node %s:%d", node.Host, node.Port),
			})
			*stepNum++
		}
		phases = append(phases, shardPhase)
	}

	// Phase N: Mongos
	mongosPhase := PlanPhase{
		Phase:       len(phases) + 1,
		Name:        "Upgrade Mongos Routers",
		Description: "Upgrade mongos instances",
		Steps:       []PlanStep{},
	}

	for _, node := range lu.config.Topology.Mongos {
		mongosPhase.Steps = append(mongosPhase.Steps, PlanStep{
			Step:        *stepNum,
			Action:      "upgrade-node",
			Target:      fmt.Sprintf("%s:%d", node.Host, node.Port),
			Description: fmt.Sprintf("Stop, replace binary, start mongos %s:%d", node.Host, node.Port),
		})
		*stepNum++
	}
	phases = append(phases, mongosPhase)

	return phases
}

// generateReplicaSetPlan generates plan for replica set
func (lu *LocalUpgrader) generateReplicaSetPlan(stepNum *int) []PlanPhase {
	phases := []PlanPhase{}

	rsPhase := PlanPhase{
		Phase:       1,
		Name:        "Upgrade Replica Set",
		Description: "Upgrade replica set nodes (secondaries first, then primary)",
		Steps:       []PlanStep{},
	}

	// Note: Actual order will be determined at runtime (secondaries first)
	for _, node := range lu.config.Topology.Mongod {
		rsPhase.Steps = append(rsPhase.Steps, PlanStep{
			Step:        *stepNum,
			Action:      "upgrade-node",
			Target:      fmt.Sprintf("%s:%d", node.Host, node.Port),
			Description: fmt.Sprintf("Stop, replace binary, start node %s:%d (runtime order: secondaries first)", node.Host, node.Port),
		})
		*stepNum++
	}

	phases = append(phases, rsPhase)
	return phases
}

// generateStandalonePlan generates plan for standalone
func (lu *LocalUpgrader) generateStandalonePlan(stepNum *int) []PlanPhase {
	phases := []PlanPhase{}

	standalonePhase := PlanPhase{
		Phase:       1,
		Name:        "Upgrade Standalone Node",
		Description: "Upgrade single MongoDB instance",
		Steps:       []PlanStep{},
	}

	node := lu.config.Topology.Mongod[0]
	standalonePhase.Steps = append(standalonePhase.Steps, PlanStep{
		Step:        *stepNum,
		Action:      "upgrade-node",
		Target:      fmt.Sprintf("%s:%d", node.Host, node.Port),
		Description: fmt.Sprintf("Stop, replace binary, start node %s:%d", node.Host, node.Port),
		Critical:    true,
	})

	phases = append(phases, standalonePhase)
	return phases
}

// PrintUpgradePlan prints the upgrade plan in a human-readable format
func PrintUpgradePlan(plan *UpgradePlan) {
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("UPGRADE PLAN - DRY RUN")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("\nCluster:        %s\n", plan.ClusterName)
	fmt.Printf("Topology:       %s\n", plan.TopologyType)
	fmt.Printf("Current Version: %s\n", plan.FromVersion)
	fmt.Printf("Target Version:  %s\n", plan.ToVersion)

	// Prerequisites
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("PREREQUISITES")
	fmt.Println(strings.Repeat("-", 70))
	for i, prereq := range plan.Prerequisites {
		fmt.Printf("%d. %s\n", i+1, prereq)
	}

	// Warnings
	if len(plan.Warnings) > 0 {
		fmt.Println("\n" + strings.Repeat("-", 70))
		fmt.Println("WARNINGS")
		fmt.Println(strings.Repeat("-", 70))
		for i, warning := range plan.Warnings {
			fmt.Printf("%d. ⚠️  %s\n", i+1, warning)
		}
	}

	// Phases
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("UPGRADE PHASES")
	fmt.Println(strings.Repeat("-", 70))

	for _, phase := range plan.Phases {
		fmt.Printf("\nPhase %d: %s\n", phase.Phase, phase.Name)
		fmt.Printf("  %s\n", phase.Description)
		fmt.Println()

		for _, step := range phase.Steps {
			critical := ""
			if step.Critical {
				critical = " [CRITICAL]"
			}
			fmt.Printf("  Step %d: %s%s\n", step.Step, step.Description, critical)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("\nThis is a dry run - no changes will be made to the cluster.")
	fmt.Println("To execute this upgrade, run the command again without --dry-run")
	fmt.Println(strings.Repeat("=", 70))
}

// Helper functions

func (lu *LocalUpgrader) getShardNames() []string {
	shardMap := make(map[string]bool)
	for _, node := range lu.config.Topology.Mongod {
		if node.ReplicaSet != "" {
			shardMap[node.ReplicaSet] = true
		}
	}

	shards := []string{}
	for shard := range shardMap {
		shards = append(shards, shard)
	}
	return shards
}

func (lu *LocalUpgrader) getNodesForShard(shardName string) []topology.MongodNode {
	nodes := []topology.MongodNode{}
	for _, node := range lu.config.Topology.Mongod {
		if node.ReplicaSet == shardName {
			nodes = append(nodes, node)
		}
	}
	return nodes
}
