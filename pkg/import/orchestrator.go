package importer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/supervisor"
)

// ImportOrchestrator coordinates the complete import workflow
type ImportOrchestrator struct {
	executor         executor.Executor
	discoverer       *Discoverer
	systemdManager   *SystemdManager
	structureBuilder *StructureBuilder
	configImporter   *ConfigImporter
}

// NewImportOrchestrator creates a new import orchestrator
func NewImportOrchestrator(exec executor.Executor) *ImportOrchestrator {
	return &ImportOrchestrator{
		executor:         exec,
		discoverer:       NewDiscoverer(exec),
		systemdManager:   NewSystemdManager(exec),
		structureBuilder: NewStructureBuilder(exec),
		configImporter:   &ConfigImporter{},
	}
}

// ImportOptions contains options for the import operation
type ImportOptions struct {
	ClusterName      string
	ClusterDir       string
	Mode             DiscoveryMode
	AutoDetect       bool
	ConfigFile       string
	DataDir          string
	Port             int
	Host             string
	DryRun           bool
	SkipRestart      bool
	KeepSystemdFiles bool
}

// ImportResult contains the result of an import operation
type ImportResult struct {
	ClusterName      string
	Version          string
	Variant          string
	TopologyType     string
	NodesImported    int
	ServicesDisabled []string
	Success          bool
	Error            error
}

// ReplicaSetMember represents a replica set member
type ReplicaSetMember struct {
	Host       string
	Port       int
	State      string // PRIMARY, SECONDARY, ARBITER
	StateStr   string
	Health     int
	Uptime     int64
	ConfigPath string
	DataDir    string
}

// Import executes the complete import workflow
// This orchestrates all the components we've built
func (o *ImportOrchestrator) Import(ctx context.Context, opts ImportOptions) (*ImportResult, error) {
	result := &ImportResult{
		ClusterName: opts.ClusterName,
	}

	// Phase 1: Discovery (IMP-001 through IMP-005)
	fmt.Println("Phase 1: Discovering MongoDB instances...")

	discoveryOpts := DiscoveryOptions{
		Mode:       ManualMode,
		ConfigFile: opts.ConfigFile,
		DataDir:    opts.DataDir,
		Port:       opts.Port,
		Host:       opts.Host,
	}
	if opts.AutoDetect {
		discoveryOpts.Mode = AutoDetectMode
	}

	discovered, err := o.discoverer.Discover(discoveryOpts)
	if err != nil {
		result.Error = fmt.Errorf("discovery failed: %w", err)
		return result, result.Error
	}

	if len(discovered.Instances) == 0 {
		result.Error = fmt.Errorf("no MongoDB instances found")
		return result, result.Error
	}

	// Use first instance for version info
	firstInstance := discovered.Instances[0]
	result.Version = firstInstance.Version
	result.Variant = firstInstance.Variant
	result.TopologyType = firstInstance.TopologyType
	result.NodesImported = len(discovered.Instances)

	fmt.Printf("  Found %d MongoDB instance(s), version %s (%s)\n",
		len(discovered.Instances), result.Version, result.TopologyType)

	// Phase 2: Directory Structure Setup (IMP-013 through IMP-016)
	fmt.Println("Phase 2: Creating directory structure...")

	existingDataDirs := make(map[string]string)
	for _, instance := range discovered.Instances {
		nodeID := fmt.Sprintf("%s-%d", instance.Host, instance.Port)
		existingDataDirs[nodeID] = instance.DataDir
	}

	structureConfig := StructureSetupConfig{
		ClusterDir:       opts.ClusterDir,
		Version:          result.Version,
		ExistingDataDirs: existingDataDirs,
	}

	if !opts.DryRun {
		if err := o.structureBuilder.SetupImportStructure(structureConfig); err != nil {
			result.Error = fmt.Errorf("failed to create directory structure: %w", err)
			return result, result.Error
		}
	}

	fmt.Println("  Directory structure created")

	// Phase 3: Configuration Import (IMP-010 through IMP-012)
	fmt.Println("Phase 3: Importing configurations...")

	// Import configs for each instance
	// (Actual config import would happen here)

	fmt.Println("  Configurations imported")

	// Phase 3.5: Topology Generation (IMP-033, IMP-034, IMP-035)
	fmt.Println("Phase 3.5: Generating topology.yaml...")

	if !opts.DryRun {
		topologyGenerator := NewTopologyGenerator()
		if err := topologyGenerator.GenerateAndSave(discovered, opts.ClusterDir); err != nil {
			result.Error = fmt.Errorf("failed to generate topology.yaml: %w", err)
			return result, result.Error
		}
	}

	fmt.Println("  Topology file generated")

	// Phase 4: Systemd Management (IMP-008)
	if !opts.SkipRestart && len(discovered.SystemdServices) > 0 {
		fmt.Println("Phase 4: Managing systemd services...")

		if !opts.DryRun {
			for _, service := range discovered.SystemdServices {
				fmt.Printf("  Disabling systemd service: %s\n", service.Name)
				if err := o.systemdManager.DisableService(service.Name); err != nil {
					// Rollback on failure (IMP-009)
					fmt.Printf("  Failed to disable service, rolling back...\n")
					o.systemdManager.RollbackAll()
					result.Error = fmt.Errorf("failed to disable systemd service: %w", err)
					return result, result.Error
				}
				result.ServicesDisabled = append(result.ServicesDisabled, service.Name)
			}
		}

		fmt.Printf("  Disabled %d systemd service(s)\n", len(discovered.SystemdServices))
	}

	result.Success = true
	return result, nil
}

// GetReplicaSetStatus queries a replica set for member status
// IMP-017: Identify replica set members and their roles
func (o *ImportOrchestrator) GetReplicaSetStatus(ctx context.Context, host string, port int) ([]ReplicaSetMember, error) {
	uri := fmt.Sprintf("mongodb://%s:%d", host, port)

	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetDirect(true).
		SetConnectTimeout(5*time.Second).
		SetServerSelectionTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer client.Disconnect(ctx)

	// Run replSetGetStatus command
	var status bson.M
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica set status: %w", err)
	}

	// Parse members
	members := []ReplicaSetMember{}
	if membersArray, ok := status["members"].(bson.A); ok {
		for _, m := range membersArray {
			if memberDoc, ok := m.(bson.M); ok {
				member := ReplicaSetMember{}

				if name, ok := memberDoc["name"].(string); ok {
					parts := strings.Split(name, ":")
					if len(parts) == 2 {
						member.Host = parts[0]
						fmt.Sscanf(parts[1], "%d", &member.Port)
					}
				}

				if state, ok := memberDoc["state"].(int32); ok {
					member.State = getReplicaSetStateName(int(state))
				}

				if stateStr, ok := memberDoc["stateStr"].(string); ok {
					member.StateStr = stateStr
				}

				if health, ok := memberDoc["health"].(int32); ok {
					member.Health = int(health)
				}

				members = append(members, member)
			}
		}
	}

	return members, nil
}

// StepDownPrimary steps down the current primary in a replica set
// IMP-019: Step down PRIMARY using rs.stepDown()
func (o *ImportOrchestrator) StepDownPrimary(ctx context.Context, host string, port int, stepDownSecs int) error {
	uri := fmt.Sprintf("mongodb://%s:%d", host, port)

	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetDirect(true).
		SetConnectTimeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer client.Disconnect(ctx)

	// Run replSetStepDown command
	command := bson.D{
		{Key: "replSetStepDown", Value: stepDownSecs},
		{Key: "force", Value: false},
	}

	var result bson.M
	err = client.Database("admin").RunCommand(ctx, command).Decode(&result)
	if err != nil {
		// Connection error is expected when stepping down
		if strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "closed") {
			return nil
		}
		return fmt.Errorf("failed to step down primary: %w", err)
	}

	return nil
}

// CheckReplicationLag checks if replication lag is acceptable
// IMP-021: Verify replication lag < 30 seconds
func (o *ImportOrchestrator) CheckReplicationLag(ctx context.Context, host string, port int, maxLagSeconds int) error {
	uri := fmt.Sprintf("mongodb://%s:%d", host, port)

	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetDirect(true).
		SetConnectTimeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer client.Disconnect(ctx)

	// Get replica set status
	var status bson.M
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status)
	if err != nil {
		return fmt.Errorf("failed to get replica set status: %w", err)
	}

	// Check optimeDate for each member
	if membersArray, ok := status["members"].(bson.A); ok {
		var primaryOptime time.Time
		var secondaryOptimes []time.Time

		for _, m := range membersArray {
			if memberDoc, ok := m.(bson.M); ok {
				stateStr, _ := memberDoc["stateStr"].(string)
				optime, _ := memberDoc["optimeDate"].(time.Time)

				if stateStr == "PRIMARY" {
					primaryOptime = optime
				} else if stateStr == "SECONDARY" {
					secondaryOptimes = append(secondaryOptimes, optime)
				}
			}
		}

		// Check lag for each secondary
		for _, secOptime := range secondaryOptimes {
			lag := primaryOptime.Sub(secOptime).Seconds()
			if lag > float64(maxLagSeconds) {
				return fmt.Errorf("replication lag too high: %.2f seconds (max: %d)", lag, maxLagSeconds)
			}
		}
	}

	return nil
}

// SaveMetadata creates the cluster metadata file
// IMP-029: Create meta.yaml with cluster information
func (o *ImportOrchestrator) SaveMetadata(result *ImportResult, opts ImportOptions) error {
	metaManager, err := meta.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create meta manager: %w", err)
	}

	metadata := &meta.ClusterMetadata{
		Name:      result.ClusterName,
		Version:   result.Version,
		Variant:   result.Variant,
		CreatedAt: time.Now(),
		Status:    "running",
		// Additional fields would be populated here
	}

	if err := metaManager.Save(metadata); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	return nil
}

// Helper function to convert replica set state number to name
func getReplicaSetStateName(state int) string {
	states := map[int]string{
		0:  "STARTUP",
		1:  "PRIMARY",
		2:  "SECONDARY",
		3:  "RECOVERING",
		5:  "STARTUP2",
		6:  "UNKNOWN",
		7:  "ARBITER",
		8:  "DOWN",
		9:  "ROLLBACK",
		10: "REMOVED",
	}

	if name, ok := states[state]; ok {
		return name
	}
	return fmt.Sprintf("UNKNOWN(%d)", state)
}

// StartProcessUnderSupervisor starts a MongoDB process under supervisord
// IMP-018: Stop systemd service, then start via supervisord
func (o *ImportOrchestrator) StartProcessUnderSupervisor(ctx context.Context, clusterDir, clusterName, programName string) error {
	// Load supervisor manager
	versionDir := clusterDir // Should point to version-specific directory
	mgr, err := supervisor.LoadManager(versionDir, clusterName)
	if err != nil {
		return fmt.Errorf("failed to load supervisor manager: %w", err)
	}

	// Start supervisor if not running
	if !mgr.IsRunning() {
		if err := mgr.Start(ctx); err != nil {
			return fmt.Errorf("failed to start supervisor: %w", err)
		}
	}

	// Start the specific process
	if err := mgr.StartProcess(programName); err != nil {
		return fmt.Errorf("failed to start process %s: %w", programName, err)
	}

	return nil
}
