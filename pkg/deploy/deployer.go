package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	variant        Variant                      // MongoDB variant
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

// NewConfigRegenerator creates a minimal Deployer for config file regeneration during upgrades
// This is used by the upgrade package to regenerate configs for the new version
func NewConfigRegenerator(clusterName, version string, variant Variant, topo *topology.Topology, metaDir, binPath string) (*Deployer, error) {
	tmplMgr, err := template.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create template manager: %w", err)
	}

	localExec := executor.NewLocalExecutor()

	return &Deployer{
		clusterName: clusterName,
		version:     version,
		variant:     variant,
		topology:    topo,
		executors:   map[string]executor.Executor{"localhost": localExec},
		metaDir:     metaDir,
		isLocal:     true,
		binPath:     binPath,
		templateMgr: tmplMgr,
	}, nil
}

// DeployConfig contains deployment configuration
type DeployConfig struct{
	ClusterName        string
	Version            string
	Variant            Variant // MongoDB variant
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

	// Validate version format
	fmt.Printf("Validating version: %s (variant: %s)...\n", cfg.Version, cfg.Variant.String())
	if err := validateVersion(cfg.Version, cfg.Variant); err != nil {
		return nil, fmt.Errorf("version validation failed: %w", err)
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
		// Monitoring files go in cluster directory
		monitoringDir := filepath.Join(metaDir, "monitoring")
		monitoringConfig := monitoring.DefaultConfig()

		// Get local executor for monitoring (monitoring runs locally even for remote clusters)
		localExec := executor.NewLocalExecutor()

		monitoringMgr, err = monitoring.NewManager(monitoringDir, monitoringConfig, localExec, supervisorMgr)
		if err != nil {
			fmt.Printf("Warning: Failed to initialize monitoring (will deploy without monitoring): %v\n", err)
			monitoringEnabled = false
		} else {
			fmt.Println("✓ Monitoring enabled (managed by cluster supervisor)")
		}
	} else {
		fmt.Println("✓ Monitoring disabled (--no-monitoring)")
	}

	fmt.Println("✓ Phase 1 complete: Topology validated")

	return &Deployer{
		clusterName:       cfg.ClusterName,
		version:           cfg.Version,
		variant:           cfg.Variant,
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

// validateVersion validates the version format based on variant
func validateVersion(version string, variant Variant) error {
	if version == "" {
		return fmt.Errorf("version cannot be empty")
	}

	parts := strings.Split(version, ".")

	switch variant {
	case VariantMongo:
		// MongoDB accepts:
		// - "X.Y" format (e.g., "7.0")
		// - "X.Y.Z" format (e.g., "7.0.5")
		if len(parts) < 2 {
			return fmt.Errorf("invalid MongoDB version format '%s': expected X.Y or X.Y.Z (e.g., '7.0' or '7.0.5')", version)
		}

		// Validate major version is numeric and >= 3
		major := parts[0]
		majorNum := 0
		if _, err := fmt.Sscanf(major, "%d", &majorNum); err != nil {
			return fmt.Errorf("invalid major version '%s': must be a number", major)
		}
		if majorNum < 3 {
			return fmt.Errorf("unsupported MongoDB major version %d: minimum supported version is 3.x", majorNum)
		}

		// Validate minor version is numeric
		minor := parts[1]
		minorNum := 0
		if _, err := fmt.Sscanf(minor, "%d", &minorNum); err != nil {
			return fmt.Errorf("invalid minor version '%s': must be a number", minor)
		}

		// If patch version provided, validate it's numeric
		if len(parts) > 2 {
			patch := parts[2]
			patchNum := 0
			if _, err := fmt.Sscanf(patch, "%d", &patchNum); err != nil {
				return fmt.Errorf("invalid patch version '%s': must be a number", patch)
			}
		}

	case VariantPercona:
		// Percona requires "X.Y.Z-R" format (e.g., "7.0.5-4")
		// Where X.Y.Z is the MongoDB version and R is the Percona release
		if !strings.Contains(version, "-") {
			return fmt.Errorf("invalid Percona version format '%s': expected X.Y.Z-R (e.g., '7.0.5-4')", version)
		}

		versionParts := strings.Split(version, "-")
		if len(versionParts) != 2 {
			return fmt.Errorf("invalid Percona version format '%s': expected X.Y.Z-R", version)
		}

		// Validate MongoDB version part (X.Y.Z)
		mongoParts := strings.Split(versionParts[0], ".")
		if len(mongoParts) < 3 {
			return fmt.Errorf("invalid Percona version format '%s': expected X.Y.Z-R (e.g., '7.0.5-4')", version)
		}

		// Validate major version
		major := mongoParts[0]
		majorNum := 0
		if _, err := fmt.Sscanf(major, "%d", &majorNum); err != nil {
			return fmt.Errorf("invalid major version '%s': must be a number", major)
		}

		// Check if major version is supported by Percona (3-8)
		if majorNum < 3 || majorNum > 8 {
			return fmt.Errorf("unsupported Percona major version %d: Percona Server for MongoDB supports versions 3.6 through 8.0", majorNum)
		}

		// Validate Percona release number
		release := versionParts[1]
		releaseNum := 0
		if _, err := fmt.Sscanf(release, "%d", &releaseNum); err != nil {
			return fmt.Errorf("invalid Percona release '%s': must be a number", release)
		}

	default:
		return fmt.Errorf("unknown variant: %s", variant.String())
	}

	return nil
}
