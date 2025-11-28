package deploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/paths"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/topology"
)

// DeployPlanner generates plans for cluster deployments
// REQ-PM-022: Uses PathResolver and ClusterLayout for consistent path management
type DeployPlanner struct {
	clusterName  string
	version      string
	variant      Variant
	topology     *topology.Topology
	executors    map[string]executor.Executor
	metaDir      string
	isLocal      bool
	binPath      string
	dryRun       bool
	layout       *paths.ClusterLayout        // REQ-PM-009 to REQ-PM-015
	pathResolver *paths.LocalPathResolver    // REQ-PM-001 to REQ-PM-003
}

// NewDeployPlanner creates a new deploy planner
// REQ-PM-022: Initializes PathResolver and ClusterLayout
func NewDeployPlanner(config *PlannerConfig) (*DeployPlanner, error) {
	// REQ-PM-009 to REQ-PM-015: Initialize cluster layout manager
	layout := paths.NewClusterLayout(config.MetaDir)

	// REQ-PM-002: Initialize local path resolver
	pathResolver := paths.NewLocalPathResolver(config.MetaDir, config.Version)

	return &DeployPlanner{
		clusterName:  config.ClusterName,
		version:      config.Version,
		variant:      config.Variant,
		topology:     config.Topology,
		executors:    config.Executors,
		metaDir:      config.MetaDir,
		isLocal:      config.IsLocal,
		binPath:      config.BinPath,
		dryRun:       config.DryRun,
		layout:       layout,
		pathResolver: pathResolver,
	}, nil
}

// PlannerConfig contains configuration for the planner
type PlannerConfig struct {
	ClusterName string
	Version     string
	Variant     Variant
	Topology    *topology.Topology
	Executors   map[string]executor.Executor
	MetaDir     string
	IsLocal     bool
	BinPath     string
	DryRun      bool
}

// GeneratePlan generates a deployment plan
func (p *DeployPlanner) GeneratePlan(ctx context.Context) (*plan.Plan, error) {
	planID := plan.NewPlanID()

	// Allocate ports for local deployments BEFORE validation and planning
	if p.isLocal {
		if err := p.allocatePorts(); err != nil {
			return nil, fmt.Errorf("failed to allocate ports: %w", err)
		}
	}

	// Run validation first
	validation, err := p.Validate(ctx)
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Create the plan
	deployPlan := &plan.Plan{
		PlanID:      planID,
		Operation:   "deploy",
		ClusterName: p.clusterName,
		CreatedAt:   time.Now(),
		Version:     p.version,
		Variant:     p.variant.String(),
		Topology:    p.topology,
		Validation:  validation,
		DryRun:      p.dryRun,
	}

	// Generate phases
	phases := []plan.PlannedPhase{
		p.generatePreparePhase(),
		p.generateDeployPhase(),
		p.generateInitializePhase(),
		p.generateFinalizePhase(),
	}
	deployPlan.Phases = phases

	// Calculate resource estimates
	deployPlan.Resources = p.calculateResources()

	return deployPlan, nil
}

// Validate performs pre-flight validation
func (p *DeployPlanner) Validate(ctx context.Context) (plan.ValidationResult, error) {
	runner := plan.NewValidationRunner(true) // Run in parallel

	// Add connectivity checks for each host
	for host, exec := range p.executors {
		// Capture loop variables for closure
		host := host
		exec := exec
		runner.AddFunc(plan.MeasuredCheckWithHost(
			"connectivity",
			host,
			func(ctx context.Context) (string, error) {
				if err := exec.CheckConnectivity(); err != nil {
					return "", err
				}
				return "Connected successfully", nil
			},
		))
	}

	// Add disk space checks
	for host, exec := range p.executors {
		// Capture loop variables for closure
		host := host
		exec := exec
		metaDir := p.metaDir // Capture for closure
		runner.AddFunc(plan.MeasuredCheckWithHost(
			"disk_space",
			host,
			func(ctx context.Context) (string, error) {
				// Check parent directory if metaDir doesn't exist yet
				// This allows validation to pass during planning phase
				checkPath := metaDir
				// Try to find the closest existing parent directory
				for checkPath != "/" && checkPath != "." {
					available, err := exec.GetDiskSpace(checkPath)
					if err == nil {
						availableGB := float64(available) / (1024 * 1024 * 1024)
						if availableGB < 10.0 {
							return "", fmt.Errorf("insufficient disk space: %.2fGB available, 10GB required", availableGB)
						}
						return fmt.Sprintf("%.2fGB available", availableGB), nil
					}
					// Try parent directory
					checkPath = filepath.Dir(checkPath)
				}
				return "", fmt.Errorf("failed to check disk space for any parent of %s", metaDir)
			},
		))
	}

	// Add port availability checks
	if p.isLocal {
		// Check all ports for local deployment
		ports := p.getAllPorts()
		// Get a local executor once (they're all the same for local)
		var localExec executor.Executor
		for _, e := range p.executors {
			localExec = e
			break
		}

		for _, port := range ports {
			// Capture loop variable for closure
			port := port
			exec := localExec
			runner.AddFunc(plan.MeasuredCheck(
				fmt.Sprintf("port_%d", port),
				func(ctx context.Context) (string, error) {
					available, err := exec.CheckPortAvailable(port)
					if err != nil {
						return "", err
					}
					if !available {
						return "", fmt.Errorf("port %d is already in use", port)
					}
					return fmt.Sprintf("Port %d is available", port), nil
				},
			))
		}
	}

	// Run all validations
	result := runner.Run(ctx)

	return result, nil
}

// generatePreparePhase generates the prepare phase operations
func (p *DeployPlanner) generatePreparePhase() plan.PlannedPhase {
	operations := make([]plan.PlannedOperation, 0)
	opIndex := 0

	// Operation: Download binary
	binaryPath := filepath.Join(p.binPath, fmt.Sprintf("mongodb-%s-%s", p.variant.String(), p.version))
	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("prepare", opIndex),
		Type:        plan.OpDownloadBinary,
		Description: fmt.Sprintf("Download MongoDB %s (%s) binary to %s", p.version, p.variant.String(), binaryPath),
		Target: plan.OperationTarget{
			Type: "binary",
			Name: fmt.Sprintf("mongodb-%s", p.version),
			Params: map[string]string{
				"version": p.version,
				"variant": p.variant.String(),
				"path":    binaryPath,
			},
		},
		Changes: []plan.Change{
			{
				ResourceType: "file",
				ResourceID:   binaryPath,
				Action:       plan.ActionCreate,
			},
		},
		Parallel: false,
	})
	opIndex++

	// REQ-PM-010: Create version-specific directory structure
	// REQ-PM-011: Data directories are version-independent
	dirsToCreate := []string{
		p.layout.BinDir(p.version),        // v<version>/bin
		p.layout.DataDir(),                 // data/ (version-independent)
		filepath.Join(p.metaDir, "tmp"),    // tmp/
	}

	// REQ-PM-011: Add version-independent data directories for each mongod node
	for _, node := range p.topology.Mongod {
		nodeDir := p.layout.NodeDataDir(node.Host, node.Port)
		dirsToCreate = append(dirsToCreate, nodeDir)
	}

	// REQ-PM-011: Add data directories for each config server
	for _, cs := range p.topology.ConfigSvr {
		nodeDir := p.layout.NodeDataDir(cs.Host, cs.Port)
		dirsToCreate = append(dirsToCreate, nodeDir)
	}

	// REQ-PM-010: Add per-node config and log directories for mongod nodes
	for _, node := range p.topology.Mongod {
		configDir, _ := p.pathResolver.ConfigDir("mongod", node.Host, node.Port)
		logDir, _ := p.pathResolver.LogDir("mongod", node.Host, node.Port)
		dirsToCreate = append(dirsToCreate, configDir, logDir)
	}

	// REQ-PM-010: Add per-node config and log directories for config servers
	for _, cs := range p.topology.ConfigSvr {
		configDir, _ := p.pathResolver.ConfigDir("config", cs.Host, cs.Port)
		logDir, _ := p.pathResolver.LogDir("config", cs.Host, cs.Port)
		dirsToCreate = append(dirsToCreate, configDir, logDir)
	}

	// REQ-PM-010: Add per-node config and log directories for mongos routers
	for _, mongos := range p.topology.Mongos {
		configDir, _ := p.pathResolver.ConfigDir("mongos", mongos.Host, mongos.Port)
		logDir, _ := p.pathResolver.LogDir("mongos", mongos.Host, mongos.Port)
		dirsToCreate = append(dirsToCreate, configDir, logDir)
	}

	// Create one operation per directory for visibility
	for _, dir := range dirsToCreate {
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("prepare", opIndex),
			Type:        plan.OpCreateDirectory,
			Description: fmt.Sprintf("Create directory: %s", dir),
			Target: plan.OperationTarget{
				Type: "directory",
				Name: dir,
			},
			Params: map[string]interface{}{
				"path": dir,
				"mode": 0755,
			},
			Changes: []plan.Change{
				{
					ResourceType: "directory",
					ResourceID:   dir,
					Action:       plan.ActionCreate,
				},
			},
			Parallel: true,
		})
		opIndex++
	}

	// Copy binaries from global storage to cluster's local bin directory
	clusterBinDir := p.layout.BinDir(p.version)
	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("prepare", opIndex),
		Type:        plan.OpCopyBinary,
		Description: fmt.Sprintf("Copy MongoDB binaries to cluster bin directory: %s", clusterBinDir),
		Target: plan.OperationTarget{
			Type: "binary",
			Name: fmt.Sprintf("mongodb-%s", p.version),
		},
		Params: map[string]interface{}{
			"source_path": binaryPath,
			"dest_dir":    clusterBinDir,
			"version":     p.version,
			"variant":     p.variant.String(),
		},
		Changes: []plan.Change{
			{
				ResourceType: "file",
				ResourceID:   filepath.Join(clusterBinDir, "mongod"),
				Action:       plan.ActionCreate,
			},
			{
				ResourceType: "file",
				ResourceID:   filepath.Join(clusterBinDir, "mongos"),
				Action:       plan.ActionCreate,
			},
			{
				ResourceType: "file",
				ResourceID:   filepath.Join(clusterBinDir, "mongosh"),
				Action:       plan.ActionCreate,
			},
		},
		Parallel: false,
	})
	opIndex++

	return plan.PlannedPhase{
		Name:              "prepare",
		Description:       "Download binaries and create directory structure",
		Order:             1,
		Operations:        operations,
		EstimatedDuration: "2 minutes",
	}
}

// generateDeployPhase generates the deploy phase operations
func (p *DeployPlanner) generateDeployPhase() plan.PlannedPhase {
	operations := make([]plan.PlannedOperation, 0)
	opIndex := 0

	// Generate config files for each config server
	for _, cs := range p.topology.ConfigSvr {
		// REQ-PM-010: Config files in version-specific paths
		configPath, _ := p.pathResolver.ConfigFile("config", cs.Host, cs.Port)
		dataDir := p.getNodeDataDir(cs.Host, cs.Port, cs.DataDir)
		logDir := p.getNodeLogDir(cs.Host, cs.Port, cs.LogDir)

		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("deploy", opIndex),
			Type:        plan.OpGenerateConfig,
			Description: fmt.Sprintf("Generate config server configuration: %s (replica set: %s, port: %d)", configPath, cs.ReplicaSet, cs.Port),
			Target: plan.OperationTarget{
				Type: "config",
				Name: fmt.Sprintf("config-%s-%d", cs.Host, cs.Port),
				Host: cs.Host,
				Port: cs.Port,
			},
			Params: map[string]interface{}{
				"config_path": configPath,
				"replica_set": cs.ReplicaSet,
				"role":        "configsvr",
				"version":     p.version,
				"variant":     p.variant.String(),
				"port":        cs.Port,
				"data_dir":    dataDir,
				"log_dir":     logDir,
				"bind_ip":     "127.0.0.1,::1", // Bind to both IPv4 and IPv6
			},
			Changes: []plan.Change{
				{
					ResourceType: "file",
					ResourceID:   configPath,
					Action:       plan.ActionCreate,
				},
			},
			Parallel: true,
		})
		opIndex++
	}

	// Generate config files for each shard mongod
	for _, node := range p.topology.Mongod {
		// REQ-PM-010: Config files in version-specific paths
		configPath, _ := p.pathResolver.ConfigFile("mongod", node.Host, node.Port)
		dataDir := p.getNodeDataDir(node.Host, node.Port, node.DataDir)
		logDir := p.getNodeLogDir(node.Host, node.Port, node.LogDir)

		rsInfo := ""
		if node.ReplicaSet != "" {
			rsInfo = fmt.Sprintf(", replica set: %s", node.ReplicaSet)
		}

		// Determine role based on cluster topology
		// Only set shardsvr role if this is part of a sharded cluster (has config servers or mongos)
		role := "standalone"
		if len(p.topology.ConfigSvr) > 0 || len(p.topology.Mongos) > 0 {
			role = "shardsvr"
		}

		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("deploy", opIndex),
			Type:        plan.OpGenerateConfig,
			Description: fmt.Sprintf("Generate mongod configuration: %s (host: %s, port: %d%s)", configPath, node.Host, node.Port, rsInfo),
			Target: plan.OperationTarget{
				Type: "mongod",
				Name: fmt.Sprintf("mongod-%s-%d", node.Host, node.Port),
				Host: node.Host,
				Port: node.Port,
			},
			Params: map[string]interface{}{
				"config_path": configPath,
				"replica_set": node.ReplicaSet,
				"role":        role,
				"version":     p.version,
				"variant":     p.variant.String(),
				"port":        node.Port,
				"data_dir":    dataDir,
				"log_dir":     logDir,
				"bind_ip":     "127.0.0.1,::1", // Bind to both IPv4 and IPv6
			},
			Changes: []plan.Change{
				{
					ResourceType: "file",
					ResourceID:   configPath,
					Action:       plan.ActionCreate,
				},
			},
			Parallel: true,
		})
		opIndex++
	}

	// Generate config files for each mongos
	for _, mongos := range p.topology.Mongos {
		// REQ-PM-010: Config files in version-specific paths
		configPath, _ := p.pathResolver.ConfigFile("mongos", mongos.Host, mongos.Port)
		logDir := p.getNodeLogDirWithType(mongos.Host, mongos.Port, mongos.LogDir, "mongos")
		configDB := p.getConfigServerConnectionString()

		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("deploy", opIndex),
			Type:        plan.OpGenerateConfig,
			Description: fmt.Sprintf("Generate mongos router configuration: %s (host: %s, port: %d)", configPath, mongos.Host, mongos.Port),
			Target: plan.OperationTarget{
				Type: "mongos",
				Name: fmt.Sprintf("mongos-%s-%d", mongos.Host, mongos.Port),
				Host: mongos.Host,
				Port: mongos.Port,
			},
			Params: map[string]interface{}{
				"config_path": configPath,
				"role":        "mongos",
				"version":     p.version,
				"variant":     p.variant.String(),
				"port":        mongos.Port,
				"log_dir":     logDir,
				"bind_ip":     "127.0.0.1,::1", // Bind to both IPv4 and IPv6
				"config_db":   configDB,
			},
			Changes: []plan.Change{
				{
					ResourceType: "file",
					ResourceID:   configPath,
					Action:       plan.ActionCreate,
				},
			},
			Parallel: true,
		})
		opIndex++
	}

	// Generate supervisor configuration
	// REQ-PM-010: Create supervisor.ini in version directory
	versionDir := p.layout.VersionDir(p.version)
	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("deploy", opIndex),
		Type:        plan.OpGenerateSupervisorCfg,
		Description: fmt.Sprintf("Generate supervisor configuration at %s/supervisor.ini", versionDir),
		Target: plan.OperationTarget{
			Type: "config",
			Name: "supervisor",
		},
		Params: map[string]interface{}{
			"cluster_dir":  versionDir,
			"cluster_name": p.clusterName,
			"version":      p.version,
			"bin_path":     p.binPath,
			"topology":     p.topology,
		},
		Changes: []plan.Change{
			{
				ResourceType: "file",
				ResourceID:   filepath.Join(versionDir, "supervisor.ini"),
				Action:       plan.ActionCreate,
			},
		},
		Parallel: false,
	})
	opIndex++

	// Start supervisord daemon
	supervisorConfigPath := filepath.Join(versionDir, "supervisor.ini")
	supervisorPort := supervisor.GetSupervisorHTTPPortForDir(versionDir)
	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("deploy", opIndex),
		Type:        plan.OpStartSupervisor,
		Description: fmt.Sprintf("Start supervisord daemon for cluster %s (port: %d)", p.clusterName, supervisorPort),
		Target: plan.OperationTarget{
			Type: "supervisor",
			Name: "supervisord",
		},
		Params: map[string]interface{}{
			"cluster_dir":  versionDir,
			"cluster_name": p.clusterName,
		},
		Changes: []plan.Change{
			{
				ResourceType: "process",
				ResourceID:   "supervisord",
				Action:       plan.ActionCreate,
			},
		},
		Parallel: false,
	})
	opIndex++

	// Start config servers first
	for _, cs := range p.topology.ConfigSvr {
		configPath, _ := p.pathResolver.ConfigFile("config", cs.Host, cs.Port)
		programName := fmt.Sprintf("mongod-%d", cs.Port)
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("deploy", opIndex),
			Type:        plan.OpStartProcess,
			Description: fmt.Sprintf("Start config server via supervisorctl: %s (replica set: %s)", programName, cs.ReplicaSet),
			Target: plan.OperationTarget{
				Type: "config",
				Name: fmt.Sprintf("config-%s-%d", cs.Host, cs.Port),
				Host: cs.Host,
				Port: cs.Port,
			},
			Params: map[string]interface{}{
				"program_name":     programName,
				"supervisor_config": supervisorConfigPath,
				"supervisor_port":   supervisorPort,
			},
			PreConditions: []plan.SafetyCheck{
				{
					ID:          fmt.Sprintf("config_exists_%d", cs.Port),
					Description: fmt.Sprintf("Config file exists: %s", configPath),
					CheckType:   "file_exists",
					Target:      configPath,
					Params: map[string]interface{}{
						"path": configPath,
					},
					Required: true,
				},
				{
					ID:          fmt.Sprintf("port_available_%d", cs.Port),
					Description: fmt.Sprintf("Port %d is available", cs.Port),
					CheckType:   "port_available",
					Target:      cs.Host,
					Params: map[string]interface{}{
						"port": float64(cs.Port),
					},
					Required: true,
				},
			},
			Changes: []plan.Change{
				{
					ResourceType: "process",
					ResourceID:   fmt.Sprintf("config-%s-%d", cs.Host, cs.Port),
					Action:       plan.ActionStart,
				},
			},
			Parallel: false,
		})
		opIndex++
	}

	// Start shard mongods
	for _, node := range p.topology.Mongod {
		configPath, _ := p.pathResolver.ConfigFile("mongod", node.Host, node.Port)
		programName := fmt.Sprintf("mongod-%d", node.Port)
		rsInfo := ""
		if node.ReplicaSet != "" {
			rsInfo = fmt.Sprintf(", replica set: %s", node.ReplicaSet)
		}
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("deploy", opIndex),
			Type:        plan.OpStartProcess,
			Description: fmt.Sprintf("Start mongod via supervisorctl: %s%s", programName, rsInfo),
			Target: plan.OperationTarget{
				Type: "mongod",
				Name: fmt.Sprintf("mongod-%s-%d", node.Host, node.Port),
				Host: node.Host,
				Port: node.Port,
			},
			Params: map[string]interface{}{
				"program_name":      programName,
				"supervisor_config": supervisorConfigPath,
				"supervisor_port":   supervisorPort,
			},
			PreConditions: []plan.SafetyCheck{
				{
					ID:          fmt.Sprintf("config_exists_%d", node.Port),
					Description: fmt.Sprintf("Config file exists: %s", configPath),
					CheckType:   "file_exists",
					Target:      configPath,
					Params: map[string]interface{}{
						"path": configPath,
					},
					Required: true,
				},
				{
					ID:          fmt.Sprintf("port_available_%d", node.Port),
					Description: fmt.Sprintf("Port %d is available", node.Port),
					CheckType:   "port_available",
					Target:      node.Host,
					Params: map[string]interface{}{
						"port": float64(node.Port),
					},
					Required: true,
				},
			},
			Changes: []plan.Change{
				{
					ResourceType: "process",
					ResourceID:   fmt.Sprintf("mongod-%s-%d", node.Host, node.Port),
					Action:       plan.ActionStart,
				},
			},
			Parallel: false,
		})
		opIndex++
	}

	// Start mongos routers last (after shards are initialized)
	for _, mongos := range p.topology.Mongos {
		configPath, _ := p.pathResolver.ConfigFile("mongos", mongos.Host, mongos.Port)
		programName := fmt.Sprintf("mongos-%d", mongos.Port)
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("deploy", opIndex),
			Type:        plan.OpStartProcess,
			Description: fmt.Sprintf("Start mongos router via supervisorctl: %s", programName),
			Target: plan.OperationTarget{
				Type: "mongos",
				Name: fmt.Sprintf("mongos-%s-%d", mongos.Host, mongos.Port),
				Host: mongos.Host,
				Port: mongos.Port,
			},
			Params: map[string]interface{}{
				"program_name":      programName,
				"supervisor_config": supervisorConfigPath,
				"supervisor_port":   supervisorPort,
			},
			PreConditions: []plan.SafetyCheck{
				{
					ID:          fmt.Sprintf("config_exists_%d", mongos.Port),
					Description: fmt.Sprintf("Config file exists: %s", configPath),
					CheckType:   "file_exists",
					Target:      configPath,
					Params: map[string]interface{}{
						"path": configPath,
					},
					Required: true,
				},
				{
					ID:          fmt.Sprintf("port_available_%d", mongos.Port),
					Description: fmt.Sprintf("Port %d is available", mongos.Port),
					CheckType:   "port_available",
					Target:      mongos.Host,
					Params: map[string]interface{}{
						"port": float64(mongos.Port),
					},
					Required: true,
				},
			},
			Changes: []plan.Change{
				{
					ResourceType: "process",
					ResourceID:   fmt.Sprintf("mongos-%s-%d", mongos.Host, mongos.Port),
					Action:       plan.ActionStart,
				},
			},
			Parallel: false,
		})
		opIndex++
	}

	return plan.PlannedPhase{
		Name:              "deploy",
		Description:       "Generate configurations and start MongoDB processes",
		Order:             2,
		Operations:        operations,
		EstimatedDuration: "1 minute",
	}
}

// generateInitializePhase generates the initialize phase operations
func (p *DeployPlanner) generateInitializePhase() plan.PlannedPhase {
	operations := make([]plan.PlannedOperation, 0)
	opIndex := 0

	// Track replica sets to initialize
	replicaSets := make(map[string][]string) // rs name -> members

	// Collect config server replica set
	if len(p.topology.ConfigSvr) > 0 {
		rsName := p.topology.ConfigSvr[0].ReplicaSet
		for _, cs := range p.topology.ConfigSvr {
			member := fmt.Sprintf("%s:%d", cs.Host, cs.Port)
			replicaSets[rsName] = append(replicaSets[rsName], member)
		}
	}

	// Collect shard replica sets
	for _, node := range p.topology.Mongod {
		if node.ReplicaSet != "" {
			member := fmt.Sprintf("%s:%d", node.Host, node.Port)
			replicaSets[node.ReplicaSet] = append(replicaSets[node.ReplicaSet], member)
		}
	}

	// Initialize each replica set
	for rsName, members := range replicaSets {
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("initialize", opIndex),
			Type:        plan.OpInitReplicaSet,
			Description: fmt.Sprintf("Initialize replica set '%s' with members: %v", rsName, members),
			Target: plan.OperationTarget{
				Type: "replica_set",
				Name: rsName,
			},
			Params: map[string]interface{}{
				"replica_set": rsName,
				"members":     members,
			},
			Changes: []plan.Change{
				{
					ResourceType: "replica_set",
					ResourceID:   rsName,
					Action:       plan.ActionCreate,
				},
			},
			Parallel: false,
		})
		opIndex++

		// Wait for replica set to stabilize
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("initialize", opIndex),
			Type:        plan.OpWaitForReady,
			Description: fmt.Sprintf("Wait for replica set '%s' to elect PRIMARY and stabilize", rsName),
			Target: plan.OperationTarget{
				Type: "replica_set",
				Name: rsName,
			},
			Params: map[string]interface{}{
				"replica_set": rsName,
				"timeout":     "2m",
			},
			Changes: []plan.Change{},
			Parallel: false,
		})
		opIndex++
	}

	// Add shards if sharded cluster
	if len(p.topology.Mongos) > 0 {
		// Group mongod nodes by replica set to create shards
		shards := make(map[string][]string)
		for _, node := range p.topology.Mongod {
			if node.ReplicaSet != "" {
				member := fmt.Sprintf("%s:%d", node.Host, node.Port)
				shards[node.ReplicaSet] = append(shards[node.ReplicaSet], member)
			}
		}

		// Add each shard to the cluster
		for rsName, members := range shards {
			shardConnStr := fmt.Sprintf("%s/%s", rsName, members[0])
			operations = append(operations, plan.PlannedOperation{
				ID:          plan.NewOperationID("initialize", opIndex),
				Type:        plan.OpAddShard,
				Description: fmt.Sprintf("Add shard '%s' to cluster (connection: %s, members: %v)", rsName, shardConnStr, members),
				Target: plan.OperationTarget{
					Type: "shard",
					Name: rsName,
				},
				Params: map[string]interface{}{
					"shard_name":        rsName,
					"connection_string": shardConnStr,
					"members":           members,
				},
				Changes: []plan.Change{
					{
						ResourceType: "shard",
						ResourceID:   rsName,
						Action:       plan.ActionCreate,
					},
				},
				Parallel: false,
			})
			opIndex++
		}

		// Verify sharding is configured
		operations = append(operations, plan.PlannedOperation{
			ID:          plan.NewOperationID("initialize", opIndex),
			Type:        plan.OpVerifyHealth,
			Description: fmt.Sprintf("Verify sharded cluster configuration (%d shards added)", len(shards)),
			Target: plan.OperationTarget{
				Type: "cluster",
				Name: p.clusterName,
			},
			Params: map[string]interface{}{
				"expected_shards": len(shards),
			},
			Changes: []plan.Change{},
			Parallel: false,
		})
		opIndex++
	}

	return plan.PlannedPhase{
		Name:              "initialize",
		Description:       "Initialize replica sets and configure sharding",
		Order:             3,
		Operations:        operations,
		EstimatedDuration: "3 minutes",
	}
}

// generateFinalizePhase generates the finalize phase operations
func (p *DeployPlanner) generateFinalizePhase() plan.PlannedPhase {
	operations := make([]plan.PlannedOperation, 0)
	opIndex := 0

	// Verify health
	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("finalize", opIndex),
		Type:        plan.OpVerifyHealth,
		Description: "Verify cluster health",
		Target: plan.OperationTarget{
			Type: "cluster",
			Name: p.clusterName,
		},
		Changes: []plan.Change{},
		Parallel: false,
	})
	opIndex++

	// Save metadata
	deployMode := "local"
	if !p.isLocal {
		deployMode = "remote"
	}

	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("finalize", opIndex),
		Type:        plan.OpSaveMetadata,
		Description: "Save cluster metadata",
		Target: plan.OperationTarget{
			Type: "cluster",
			Name: p.clusterName,
		},
		Params: map[string]interface{}{
			"version":     p.version,
			"variant":     p.variant.String(),
			"bin_path":    p.binPath,
			"deploy_mode": deployMode,
			"topology":    p.topology,
		},
		Changes: []plan.Change{
			{
				ResourceType: "file",
				ResourceID:   "meta.yaml",
				Action:       plan.ActionCreate,
			},
		},
		Parallel: false,
	})
	opIndex++

	// REQ-PM-012: Create current symlink to activate this version
	operations = append(operations, plan.PlannedOperation{
		ID:          plan.NewOperationID("finalize", opIndex),
		Type:        plan.OpCreateSymlink,
		Description: fmt.Sprintf("Create 'current' symlink -> v%s", p.version),
		Target: plan.OperationTarget{
			Type: "symlink",
			Name: "current",
		},
		Params: map[string]interface{}{
			"link_path":   p.layout.CurrentLink(),
			"target_path": fmt.Sprintf("v%s", p.version),
			"version":     p.version,
		},
		Changes: []plan.Change{
			{
				ResourceType: "symlink",
				ResourceID:   "current",
				Action:       plan.ActionCreate,
			},
		},
		Parallel: false,
	})

	return plan.PlannedPhase{
		Name:              "finalize",
		Description:       "Verify health, save metadata, and activate version",
		Order:             4,
		Operations:        operations,
		EstimatedDuration: "30 seconds",
	}
}

// calculateResources calculates resource estimates for the deployment
func (p *DeployPlanner) calculateResources() plan.ResourceEstimate {
	estimate := plan.ResourceEstimate{
		Hosts:            len(p.executors),
		TotalProcesses:   0,
		PortsUsed:        p.getAllPorts(),
		DiskSpaceGB:      10.0, // Estimate
		DownloadSizeMB:   120,  // Typical MongoDB binary size
		ProcessesPerHost: make(map[string]int),
	}

	// Count processes based on topology
	estimate.TotalProcesses += len(p.topology.Mongod)
	for _, node := range p.topology.Mongod {
		estimate.ProcessesPerHost[node.Host]++
	}

	estimate.TotalProcesses += len(p.topology.ConfigSvr)
	for _, cs := range p.topology.ConfigSvr {
		estimate.ProcessesPerHost[cs.Host]++
	}

	estimate.TotalProcesses += len(p.topology.Mongos)
	for _, mongos := range p.topology.Mongos {
		estimate.ProcessesPerHost[mongos.Host]++
	}

	return estimate
}

// getAllPorts collects all ports used in the topology
func (p *DeployPlanner) getAllPorts() []int {
	ports := make([]int, 0)

	for _, node := range p.topology.Mongod {
		if node.Port > 0 { // Skip port 0 (auto-allocated)
			ports = append(ports, node.Port)
		}
	}

	for _, cs := range p.topology.ConfigSvr {
		if cs.Port > 0 {
			ports = append(ports, cs.Port)
		}
	}

	for _, mongos := range p.topology.Mongos {
		if mongos.Port > 0 {
			ports = append(ports, mongos.Port)
		}
	}

	return ports
}

// getNodeDataDir gets the data directory for a node
// REQ-PM-011: Data directories are version-independent
func (p *DeployPlanner) getNodeDataDir(host string, port int, defaultDir string) string {
	if p.isLocal {
		// REQ-PM-011: Use ClusterLayout for version-independent data directory
		return p.layout.NodeDataDir(host, port)
	}
	return filepath.Join(defaultDir, fmt.Sprintf("mongod-%d", port))
}

// getNodeLogDir gets the log directory for a mongod node
func (p *DeployPlanner) getNodeLogDir(host string, port int, defaultDir string) string {
	return p.getNodeLogDirWithType(host, port, defaultDir, "mongod")
}

// getNodeLogDirWithType gets log directory for a node with explicit type (mongod/mongos)
// REQ-PM-010: Log directories are version-specific
func (p *DeployPlanner) getNodeLogDirWithType(host string, port int, defaultDir string, nodeType string) string {
	if p.isLocal {
		// REQ-PM-010: Use PathResolver for version-specific log directory
		logDir, _ := p.pathResolver.LogDir(nodeType, host, port)
		return logDir
	}
	return filepath.Join(defaultDir, fmt.Sprintf("%s-%d", nodeType, port))
}

// getConfigServerConnectionString returns the connection string for config servers
func (p *DeployPlanner) getConfigServerConnectionString() string {
	if len(p.topology.ConfigSvr) == 0 {
		return ""
	}

	// Get the replica set name from the first config server
	rsName := p.topology.ConfigSvr[0].ReplicaSet

	// Build connection string
	members := make([]string, 0, len(p.topology.ConfigSvr))
	for _, cs := range p.topology.ConfigSvr {
		members = append(members, fmt.Sprintf("%s:%d", cs.Host, cs.Port))
	}

	return fmt.Sprintf("%s/%s", rsName, strings.Join(members, ","))
}

// allocatePorts allocates ports for all nodes with port 0 in the topology
func (p *DeployPlanner) allocatePorts() error {
	if !p.isLocal {
		return nil
	}

	// Use the topology port allocator
	return topology.AllocatePortsForTopology(p.topology, nil)
}
