package importer

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/zph/mup/pkg/executor"
)

// DiscoveryMode represents the mode of discovery
type DiscoveryMode string

const (
	AutoDetectMode DiscoveryMode = "auto-detect"
	ManualMode     DiscoveryMode = "manual"
)

// DiscoveryOptions contains options for discovery
type DiscoveryOptions struct {
	Mode       DiscoveryMode
	ConfigFile string // For manual mode
	DataDir    string // For manual mode
	Port       int    // For manual mode
	Host       string // For manual mode (default: localhost)
}

// DiscoveryResult contains the results of discovery
type DiscoveryResult struct {
	Mode            DiscoveryMode
	Instances       []MongoInstance
	SystemdServices []SystemdService
}

// MongoInstance represents a discovered MongoDB instance
type MongoInstance struct {
	Host         string
	Port         int
	PID          int
	ConfigFile   string
	DataDir      string
	LogPath      string
	Version      string
	Variant      string // "mongo" or "percona"
	TopologyType string // "standalone", "replica_set", "sharded"
	ReplicaSet   string // Replica set name if applicable
	ProcessType  string // "mongod", "mongos", "configsvr", "shardsvr"
}

// SystemdService represents a discovered systemd service
type SystemdService struct {
	Name     string
	UnitFile string
	Status   string // "active", "inactive", etc.
}

// Process represents a running process
type Process struct {
	PID     int
	Command string
	User    string
}

// Discoverer handles discovery of MongoDB instances
type Discoverer struct {
	executor executor.Executor
}

// NewDiscoverer creates a new Discoverer
func NewDiscoverer(exec executor.Executor) *Discoverer {
	return &Discoverer{
		executor: exec,
	}
}

// Discover performs discovery based on the provided options
// IMP-001: Support both auto-detect and manual modes
func (d *Discoverer) Discover(opts DiscoveryOptions) (*DiscoveryResult, error) {
	if opts.Mode == ManualMode {
		// IMP-003: Use provided config paths in manual mode
		return d.discoverManual(opts)
	}

	// IMP-002: Scan for MongoDB processes when auto-detect is requested
	return d.discoverAuto()
}

// discoverManual performs manual discovery with explicit configuration
func (d *Discoverer) discoverManual(opts DiscoveryOptions) (*DiscoveryResult, error) {
	host := opts.Host
	if host == "" {
		host = "localhost"
	}

	instance := MongoInstance{
		Host:       host,
		Port:       opts.Port,
		ConfigFile: opts.ConfigFile,
		DataDir:    opts.DataDir,
	}

	// Try to query MongoDB for additional info
	info, err := d.queryMongoDBInfo(instance)
	if err == nil {
		instance.Version = info.Version
		instance.Variant = info.Variant
		instance.TopologyType = info.TopologyType
		instance.ReplicaSet = info.ReplicaSet
	}

	result := &DiscoveryResult{
		Mode:      ManualMode,
		Instances: []MongoInstance{instance},
	}

	return result, nil
}

// discoverAuto performs automatic discovery
func (d *Discoverer) discoverAuto() (*DiscoveryResult, error) {
	result := &DiscoveryResult{
		Mode:            AutoDetectMode,
		Instances:       []MongoInstance{},
		SystemdServices: []SystemdService{},
	}

	// IMP-002: Scan for running MongoDB processes
	processes, err := d.scanProcesses()
	if err != nil {
		return nil, fmt.Errorf("failed to scan processes: %w", err)
	}

	// Convert processes to instances
	for _, proc := range processes {
		instance, err := d.processToInstance(proc)
		if err != nil {
			// Log warning but continue
			continue
		}

		// Query MongoDB for additional info
		info, err := d.queryMongoDBInfo(instance)
		if err == nil {
			instance.Version = info.Version
			instance.Variant = info.Variant
			instance.TopologyType = info.TopologyType
			instance.ReplicaSet = info.ReplicaSet
		}

		result.Instances = append(result.Instances, instance)
	}

	// IMP-004: Detect systemd services
	services, err := d.detectSystemdServices()
	if err == nil {
		result.SystemdServices = services
	}

	if len(result.Instances) == 0 {
		return nil, fmt.Errorf("no MongoDB instances found")
	}

	return result, nil
}

// scanProcesses scans for running MongoDB processes
// IMP-002: Scan for MongoDB processes
func (d *Discoverer) scanProcesses() ([]Process, error) {
	// Use ps to find mongod and mongos processes
	output, err := d.executor.Execute("ps aux | grep -E 'mongod|mongos' | grep -v grep")
	if err != nil {
		return nil, fmt.Errorf("no MongoDB processes found")
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	processes := []Process{}

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse ps output: USER PID ... COMMAND
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}

		// Get full command (everything from field 10 onwards typically)
		var command string
		if len(fields) > 10 {
			command = strings.Join(fields[10:], " ")
		} else {
			command = strings.Join(fields[2:], " ")
		}

		processes = append(processes, Process{
			PID:     pid,
			Command: command,
			User:    fields[0],
		})
	}

	return processes, nil
}

// processToInstance converts a Process to a MongoInstance
func (d *Discoverer) processToInstance(proc Process) (MongoInstance, error) {
	instance := MongoInstance{
		PID: proc.PID,
	}

	// Parse command line for --config, --port, --dbpath, --logpath
	configRegex := regexp.MustCompile(`--config[= ]([^\s]+)`)
	portRegex := regexp.MustCompile(`--port[= ](\d+)`)
	dbpathRegex := regexp.MustCompile(`--dbpath[= ]([^\s]+)`)
	logpathRegex := regexp.MustCompile(`--logpath[= ]([^\s]+)`)
	bindIpRegex := regexp.MustCompile(`--bind_ip[= ]([^\s]+)`)

	if matches := configRegex.FindStringSubmatch(proc.Command); len(matches) > 1 {
		instance.ConfigFile = matches[1]
	}

	if matches := portRegex.FindStringSubmatch(proc.Command); len(matches) > 1 {
		port, _ := strconv.Atoi(matches[1])
		instance.Port = port
	} else {
		instance.Port = 27017 // Default MongoDB port
	}

	if matches := dbpathRegex.FindStringSubmatch(proc.Command); len(matches) > 1 {
		instance.DataDir = matches[1]
	}

	if matches := logpathRegex.FindStringSubmatch(proc.Command); len(matches) > 1 {
		instance.LogPath = matches[1]
	}

	if matches := bindIpRegex.FindStringSubmatch(proc.Command); len(matches) > 1 {
		instance.Host = matches[1]
		// If bind_ip is 0.0.0.0 or ::, use localhost for connection
		if instance.Host == "0.0.0.0" || instance.Host == "::" {
			instance.Host = "localhost"
		}
	} else {
		instance.Host = "localhost"
	}

	return instance, nil
}

// detectSystemdServices detects systemd services for MongoDB
// IMP-004: Detect associated systemd service units
func (d *Discoverer) detectSystemdServices() ([]SystemdService, error) {
	// List systemd services matching mongod* or mongos*
	output, err := d.executor.Execute("systemctl list-units --type=service --all | grep -E 'mongo[ds]' || true")
	if err != nil {
		// Not an error if no services found
		return []SystemdService{}, nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	services := []SystemdService{}

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse systemctl output: NAME LOAD ACTIVE SUB DESCRIPTION
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		serviceName := fields[0]
		// Remove .service suffix if present
		serviceName = strings.TrimSuffix(serviceName, ".service")

		status := fields[2] // ACTIVE column

		// Get unit file path
		unitFileOutput, err := d.executor.Execute(fmt.Sprintf("systemctl show -p FragmentPath %s", serviceName))
		if err != nil {
			continue
		}

		// Parse FragmentPath=...
		unitFile := ""
		if strings.HasPrefix(unitFileOutput, "FragmentPath=") {
			unitFile = strings.TrimPrefix(strings.TrimSpace(unitFileOutput), "FragmentPath=")
		}

		services = append(services, SystemdService{
			Name:     serviceName,
			UnitFile: unitFile,
			Status:   status,
		})
	}

	return services, nil
}

// MongoDBInfo contains MongoDB instance information
type MongoDBInfo struct {
	Version      string
	Variant      string
	TopologyType string
	ReplicaSet   string
}

// queryMongoDBInfo queries MongoDB for version and topology information
// IMP-005: Query MongoDB for version, variant, and topology information
func (d *Discoverer) queryMongoDBInfo(instance MongoInstance) (MongoDBInfo, error) {
	info := MongoDBInfo{}

	// Build connection string
	uri := fmt.Sprintf("mongodb://%s:%d", instance.Host, instance.Port)

	// Connect to MongoDB with short timeout (2 seconds) to avoid hanging in tests
	ctx := context.Background()
	clientOpts := options.Client().
		ApplyURI(uri).
		SetDirect(true).
		SetConnectTimeout(2 * time.Second).
		SetServerSelectionTimeout(2 * time.Second).
		SetSocketTimeout(2 * time.Second)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return info, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer client.Disconnect(ctx)

	// Get server info
	var buildInfo bson.M
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&buildInfo)
	if err != nil {
		return info, fmt.Errorf("failed to get buildInfo: %w", err)
	}

	// Extract version
	if version, ok := buildInfo["version"].(string); ok {
		info.Version = version
	}

	// Detect variant (Percona includes "Percona" in modules or version string)
	if modules, ok := buildInfo["modules"].(bson.A); ok {
		for _, mod := range modules {
			if modStr, ok := mod.(string); ok {
				if strings.Contains(strings.ToLower(modStr), "percona") {
					info.Variant = "percona"
					break
				}
			}
		}
	}
	if info.Variant == "" {
		versionStr := fmt.Sprintf("%v", buildInfo["version"])
		if strings.Contains(strings.ToLower(versionStr), "percona") {
			info.Variant = "percona"
		} else {
			info.Variant = "mongo"
		}
	}

	// Detect topology type
	var ismaster bson.M
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}}).Decode(&ismaster)
	if err != nil {
		// isMaster failed, might be using older MongoDB
		err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "ismaster", Value: 1}}).Decode(&ismaster)
		if err != nil {
			return info, fmt.Errorf("failed to get isMaster: %w", err)
		}
	}

	// Check for sharding (mongos)
	if msg, ok := ismaster["msg"].(string); ok && msg == "isdbgrid" {
		info.TopologyType = "sharded"
		return info, nil
	}

	// Check for replica set
	if setName, ok := ismaster["setName"].(string); ok && setName != "" {
		info.TopologyType = "replica_set"
		info.ReplicaSet = setName
		return info, nil
	}

	// Standalone
	info.TopologyType = "standalone"
	return info, nil
}
