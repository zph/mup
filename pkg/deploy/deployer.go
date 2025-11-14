package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/monitoring"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/template"
	"github.com/zph/mup/pkg/topology"
)

// Deployer manages the deployment of MongoDB clusters
type Deployer struct {
	clusterName    string
	version        string
	topology       *topology.Topology
	executors      map[string]executor.Executor // host -> executor
	metaDir        string                       // cluster metadata directory
	isLocal        bool                         // local vs remote deployment
	binPath        string                       // Path to MongoDB binaries (from binary manager)
	templateMgr    *template.Manager            // Template manager for config generation
	supervisorMgr  *supervisor.Manager          // Supervisor manager for process management
	monitoringMgr  *monitoring.Manager          // Monitoring manager
	monitoringEnabled bool                      // Whether monitoring is enabled
}

// DeployConfig contains deployment configuration
type DeployConfig struct {
	ClusterName        string
	Version            string
	TopologyFile       string
	SSHUser            string
	IdentityFile       string
	SkipConfirm        bool
	DisableMonitoring  bool // Disable monitoring deployment
}

// NewDeployer creates a new deployer
func NewDeployer(cfg DeployConfig) (*Deployer, error) {
	// Phase 1: Parse & Validate
	fmt.Println("Phase 1: Parse & Validate")
	fmt.Println("==========================")

	// Parse topology file
	fmt.Printf("Parsing topology file: %s\n", cfg.TopologyFile)
	topo, err := topology.ParseTopologyFile(cfg.TopologyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to parse topology: %w", err)
	}

	// Validate topology
	fmt.Println("Validating topology...")
	if err := topo.Validate(); err != nil {
		return nil, fmt.Errorf("topology validation failed: %w", err)
	}

	// Detect deployment mode
	isLocal := topo.IsLocalDeployment()

	// Create executors first (needed for port checking)
	executors := make(map[string]executor.Executor)
	hosts := topo.GetAllHosts()

	if isLocal {
		fmt.Println("Deployment mode: LOCAL (all nodes on localhost)")

		// Create local executor
		for _, host := range hosts {
			executors[host] = executor.NewLocalExecutor()
		}

		// Allocate ports for local deployment with availability checking
		fmt.Println("Allocating ports for local deployment...")

		if err := topology.AllocatePortsForTopology(topo, nil); err != nil {
			return nil, fmt.Errorf("failed to allocate ports: %w", err)
		}
	} else {
		fmt.Println("Deployment mode: REMOTE (SSH-based deployment)")
		// TODO: Create SSHExecutors when SSH support is implemented
		return nil, fmt.Errorf("remote deployment not yet implemented")
	}

	// Print topology summary
	topoType := topo.GetTopologyType()
	fmt.Printf("Topology type: %s\n", topoType)
	fmt.Printf("Total nodes: mongod=%d, mongos=%d, config=%d\n",
		len(topo.Mongod), len(topo.Mongos), len(topo.ConfigSvr))

	// Validate MongoDB version
	fmt.Printf("MongoDB version: %s\n", cfg.Version)
	if err := validateMongoVersion(cfg.Version); err != nil {
		return nil, fmt.Errorf("invalid MongoDB version: %w", err)
	}

	// Executors already created above (needed for port checking)
	fmt.Printf("Using %d executor(s)\n", len(executors))

	// Determine metadata directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	metaDir := filepath.Join(homeDir, ".mup", "storage", "clusters", cfg.ClusterName)

	// Initialize template manager
	templateMgr, err := template.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize template manager: %w", err)
	}

	// Initialize supervisor manager
	supervisorMgr, err := supervisor.NewManager(metaDir, cfg.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize supervisor manager: %w", err)
	}

	// Initialize monitoring manager if enabled
	var monitoringMgr *monitoring.Manager
	monitoringEnabled := !cfg.DisableMonitoring

	if monitoringEnabled {
		monitoringDir := filepath.Join(homeDir, ".mup", "monitoring")
		monitoringConfig := monitoring.DefaultConfig()

		// Get local executor for monitoring (monitoring runs locally even for remote clusters)
		localExec := executor.NewLocalExecutor()

		monitoringMgr, err = monitoring.NewManager(monitoringDir, monitoringConfig, localExec)
		if err != nil {
			fmt.Printf("Warning: Failed to initialize monitoring (will deploy without monitoring): %v\n", err)
			monitoringEnabled = false
		} else {
			fmt.Println("✓ Monitoring enabled")
		}
	} else {
		fmt.Println("✓ Monitoring disabled (--no-monitoring)")
	}

	fmt.Println("✓ Phase 1 complete: Topology validated")

	return &Deployer{
		clusterName:       cfg.ClusterName,
		version:           cfg.Version,
		topology:          topo,
		executors:         executors,
		metaDir:           metaDir,
		isLocal:           isLocal,
		templateMgr:       templateMgr,
		supervisorMgr:     supervisorMgr,
		monitoringMgr:     monitoringMgr,
		monitoringEnabled: monitoringEnabled,
	}, nil
}

// Deploy executes the full deployment workflow
func (d *Deployer) Deploy(ctx context.Context) error {
	// Phase 2: Prepare
	if err := d.prepare(ctx); err != nil {
		return fmt.Errorf("phase 2 (prepare) failed: %w", err)
	}

	// Phase 3: Deploy
	if err := d.deploy(ctx); err != nil {
		return fmt.Errorf("phase 3 (deploy) failed: %w", err)
	}

	// Phase 4: Initialize
	if err := d.initialize(ctx); err != nil {
		return fmt.Errorf("phase 4 (initialize) failed: %w", err)
	}

	// Phase 4.5: Deploy Monitoring (if enabled)
	if d.monitoringEnabled {
		if err := d.deployMonitoring(ctx); err != nil {
			fmt.Printf("Warning: Monitoring deployment failed (non-fatal): %v\n", err)
			// Don't fail the entire deployment if monitoring fails
		}
	}

	// Phase 5: Finalize
	if err := d.finalize(ctx); err != nil {
		return fmt.Errorf("phase 5 (finalize) failed: %w", err)
	}

	return nil
}

// Close closes all executors
func (d *Deployer) Close() error {
	for _, exec := range d.executors {
		if err := exec.Close(); err != nil {
			return err
		}
	}
	return nil
}

// validateMongoVersion validates the MongoDB version format
func validateMongoVersion(version string) error {
	// Simple validation - should be X.Y or X.Y.Z format
	// TODO: Add more comprehensive version validation
	if version == "" {
		return fmt.Errorf("version cannot be empty")
	}
	return nil
}
