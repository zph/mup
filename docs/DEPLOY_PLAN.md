# Mup Cluster Deploy - Implementation Plan

This document outlines the design and implementation plan for `mup cluster deploy`, supporting both local deployments (with varied ports) and remote VM deployments (with standard ports).

## Command Overview

```bash
# Remote deployment (standard ports)
mup cluster deploy <cluster-name> <version> <topology-file> [flags]

# Local deployment (varied ports, auto-allocated)
mup cluster deploy <cluster-name> <version> <topology-file> --local [flags]
```

## Design Principles (from TiUP)

TiUP breaks deployment into distinct phases for robustness and clarity:

1. **Parse & Validate** - Parse topology, validate configuration
2. **Prepare** - Download binaries, check prerequisites
3. **Deploy** - Distribute files, create directories, generate configs
4. **Initialize** - Start services, initialize cluster
5. **Finalize** - Save state, report status

This phased approach allows:
- Clear error reporting at each stage
- Easy rollback on failure
- Resume capability (future enhancement)
- Progress visibility for users

## Unified Executor Interface

**Critical Design Principle:** Local and remote deployments must use an identical interface to enable easy testing and code reuse. Both modes implement the same `Executor` interface, with local execution using direct OS calls and remote using SSH.

```go
// pkg/executor/executor.go

// Executor provides a unified interface for both local and remote operations
type Executor interface {
    // File Operations
    CreateDirectory(path string, mode os.FileMode) error
    UploadFile(localPath, remotePath string) error
    UploadContent(content []byte, remotePath string) error
    DownloadFile(remotePath, localPath string) error
    FileExists(path string) (bool, error)
    RemoveFile(path string) error
    RemoveDirectory(path string) error

    // Command Execution
    Execute(command string) (output string, err error)
    ExecuteWithInput(command string, stdin io.Reader) (output string, err error)
    Background(command string) (pid int, err error)

    // Process Management
    IsProcessRunning(pid int) (bool, error)
    KillProcess(pid int) error

    // System Information
    GetOSInfo() (*OSInfo, error)
    GetDiskSpace(path string) (available uint64, err error)
    CheckPortAvailable(port int) (bool, error)
    UserExists(username string) (bool, error)

    // Connection Management
    CheckConnectivity() error
    Close() error
}

// LocalExecutor implements Executor for local operations
type LocalExecutor struct {
    workDir string
}

// SSHExecutor implements Executor for remote operations via SSH
type SSHExecutor struct {
    host     string
    port     int
    user     string
    keyPath  string
    client   *ssh.Client
}
```

### Why This Matters

1. **Testability**: Write tests once against the Executor interface, swap implementations
2. **Code Reuse**: Deployer code doesn't need mode-specific branches
3. **Flexibility**: Easy to add new executor types (e.g., Docker, Kubernetes)
4. **Consistency**: Identical behavior guarantees between local and remote

### Example Usage

```go
// Deployer doesn't care about mode
type Deployer struct {
    executor Executor
    topology *Topology
}

func (d *Deployer) DeployBinaries(sourcePath, targetPath string) error {
    // Works identically for local or remote
    if err := d.executor.CreateDirectory(targetPath, 0755); err != nil {
        return err
    }

    if err := d.executor.UploadFile(sourcePath, targetPath); err != nil {
        return err
    }

    return d.executor.Execute(fmt.Sprintf("chmod +x %s/bin/*", targetPath))
}

// Factory creates appropriate executor
func NewExecutor(mode DeploymentMode, host string, config *SSHConfig) (Executor, error) {
    if mode == ModeLocal {
        return &LocalExecutor{workDir: config.WorkDir}, nil
    }
    return NewSSHExecutor(host, config)
}
```

### Testing Benefits

```go
// Mock executor for unit tests
type MockExecutor struct {
    commands []string
    files    map[string][]byte
}

func TestDeployBinaries(t *testing.T) {
    mock := &MockExecutor{
        files: make(map[string][]byte),
    }

    deployer := &Deployer{executor: mock}

    // Test without SSH or local filesystem
    err := deployer.DeployBinaries("/tmp/mongo.tgz", "/opt/mongodb")

    assert.NoError(t, err)
    assert.Contains(t, mock.commands, "chmod +x /opt/mongodb/bin/*")
    assert.True(t, mock.files["/opt/mongodb/mongo.tgz"] != nil)
}
```

---

## Deployment Modes

### Mode 1: Remote Deployment (Default)

**Use Case:** Deploy to multiple VMs for production/staging
**Port Strategy:** Use standard MongoDB ports (27017 for mongod, 27016 for config servers, 27015 for mongos)
**Host Detection:** Each node has unique `host` field in topology

**Example Topology:**
```yaml
global:
  user: mongodb
  ssh_port: 22
  deploy_dir: /opt/mup/mongodb
  data_dir: /data/mongodb
  log_dir: /var/log/mongodb

mongod_servers:
  - host: 192.168.1.10
    # port defaults to 27017
  - host: 192.168.1.11
  - host: 192.168.1.12

replica_set:
  name: rs0
```

### Mode 2: Local Deployment (--local flag)

**Use Case:** Local testing, CI/CD, development
**Port Strategy:** Auto-allocate sequential available ports starting from configurable base (default: 30000)
**Host Detection:** All nodes use `localhost` or `127.0.0.1`

**Example Topology:**
```yaml
global:
  user: $USER  # Current user
  deploy_dir: ~/.mup/local-clusters/test-cluster
  data_dir: ~/.mup/local-clusters/test-cluster/data
  log_dir: ~/.mup/local-clusters/test-cluster/logs

mongod_servers:
  - host: localhost
  - host: localhost
  - host: localhost

replica_set:
  name: rs0
```

**Port Allocation:** Automatically assigns:
- mongod nodes: 30000, 30001, 30002
- config servers: 30010, 30011, 30012
- mongos routers: 30020, 30021

---

## Phase-by-Phase Implementation

### Phase 1: Parse & Validate

**Package:** `pkg/topology/`

**Tasks:**
1. Parse topology YAML file
2. Detect deployment mode (local vs remote)
3. Validate topology structure
4. Apply defaults from global section
5. Validate node specifications
6. Check for conflicts (duplicate hosts+ports)

**Detection Logic:**
```go
func (t *Topology) DetectMode() DeploymentMode {
    allLocal := true
    for _, node := range t.AllNodes() {
        if !isLocalhost(node.Host) {
            allLocal = false
            break
        }
    }

    if allLocal {
        return ModeLocal
    }
    return ModeRemote
}

func isLocalhost(host string) bool {
    return host == "localhost" ||
           host == "127.0.0.1" ||
           host == "::1" ||
           host == "0.0.0.0"
}
```

**Validation Checks:**
- Required fields present (cluster_name, version, at least one node)
- MongoDB version is supported (3.6-8.0)
- Replica set has odd number of nodes (for voting)
- Paths are absolute (or relative for local mode)
- No port conflicts within topology
- SSH connectivity for remote nodes
- Local disk space and permissions for local mode

**Output:** `ValidatedTopology` struct with enriched node information

---

### Phase 2: Prepare

**Package:** `pkg/deploy/prepare.go`

**Tasks:**

#### 2.1 Binary Management

**Standalone binary manager implementation:**

The binary manager handles:
- Downloading MongoDB binaries from official sources
- Caching binaries in `~/.mup/storage/packages/{version}-{os}-{arch}/bin`
- Version resolution (X.Y -> latest X.Y.Z patch)
- Multi-platform support (darwin/linux, x86_64/arm64)

```go
// pkg/deploy/prepare.go

// prepareBinaries ensures MongoDB binaries are available for all required platforms
func (d *Deployer) prepareBinaries(ctx context.Context) error {
    fmt.Println("Preparing MongoDB binaries...")

    // Create binary manager
    bm, err := NewBinaryManager()
    if err != nil {
        return fmt.Errorf("failed to create binary manager: %w", err)
    }
    defer bm.Close()

    // Collect all unique platforms from hosts
    platforms, err := d.CollectPlatforms(ctx)
    if err != nil {
        return fmt.Errorf("failed to collect platforms: %w", err)
    }

    // Fetch binaries for each platform in parallel
    binPaths := make(map[string]string) // platformKey -> binPath
    var mu sync.Mutex
    var wg sync.WaitGroup

    for platform := range platforms {
        wg.Add(1)
        go func(p Platform) {
            defer wg.Done()
            binPath, err := bm.GetBinPathForPlatform(d.version, p)
            if err != nil {
                fmt.Printf("  Warning: failed to get binaries for %s: %v\n", p.Key(), err)
                return
            }
            mu.Lock()
            binPaths[p.Key()] = binPath
            mu.Unlock()
        }(platform)
    }
    wg.Wait()

    // Store bin path for deployment
    // For local: use current platform's bin path
    // For remote: will distribute correct binaries per host in Phase 3
    if d.isLocal {
        currentPlatform := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
        if binPath, ok := binPaths[currentPlatform.Key()]; ok {
            d.binPath = binPath
        }
    }

    fmt.Printf("  ✓ MongoDB %s binaries ready\n", d.version)
    return nil
}
```

**Key Benefits:**
- Standalone binary management (no external dependencies)
- Automatic version resolution (4.0 -> 4.0.28)
- Caching reduces download time on subsequent deployments
- Multi-platform support (downloads binaries for any OS/arch combination)
- Uses MongoDB's official full.json API for accurate version information

**Binary Storage:**
- Local: `~/.mup/storage/packages/{version}-{os}-{arch}/bin/`
- Remote: Will be distributed to `/opt/mup/mongodb/{version}/bin/` in Phase 3

#### 2.2 Pre-flight Checks (Unified Interface)

**Using the same Executor interface for both modes:**

```go
// pkg/deploy/prepare.go

// PreflightIssue represents a pre-flight check issue
type PreflightIssue struct {
    Severity string // "error", "warning"
    Message  string
    Node     string // host:port
}

// preflightChecks performs comprehensive pre-deployment validation
func (d *Deployer) preflightChecks(ctx context.Context) error {
    fmt.Println("Running pre-flight checks...")

    var allIssues []PreflightIssue
    var mu sync.Mutex
    var wg sync.WaitGroup

    // Collect all unique hosts
    hosts := d.topology.GetAllHosts()

    // Check each host in parallel
    for _, host := range hosts {
        wg.Add(1)
        go func(h string) {
            defer wg.Done()
            executor := d.executors[h]
            issues := d.checkHost(h, executor)

            mu.Lock()
            allIssues = append(allIssues, issues...)
            mu.Unlock()
        }(host)
    }

    wg.Wait()

    // Separate errors from warnings
    var errors []PreflightIssue
    var warnings []PreflightIssue

    for _, issue := range allIssues {
        if issue.Severity == "error" {
            errors = append(errors, issue)
        } else {
            warnings = append(warnings, issue)
        }
    }

    // Display warnings
    if len(warnings) > 0 {
        fmt.Println("\n  Warnings:")
        for _, w := range warnings {
            fmt.Printf("    - %s: %s\n", w.Node, w.Message)
        }
    }

    // Fail on errors
    if len(errors) > 0 {
        fmt.Println("\n  Errors:")
        for _, e := range errors {
            fmt.Printf("    ✗ %s: %s\n", e.Node, e.Message)
        }
        return fmt.Errorf("pre-flight checks failed with %d error(s)", len(errors))
    }

    fmt.Println("  ✓ Pre-flight checks passed")
    return nil
}

// checkHost performs all checks for a single host
func (d *Deployer) checkHost(host string, exec executor.Executor) []PreflightIssue {
    issues := []PreflightIssue{}

    // 1. Connectivity check
    if err := exec.CheckConnectivity(); err != nil {
        issues = append(issues, PreflightIssue{
            Severity: "error",
            Message:  fmt.Sprintf("Cannot connect: %v", err),
            Node:     host,
        })
        return issues // Fatal, can't continue
    }

    // 2. OS information
    osInfo, err := exec.GetOSInfo()
    if err != nil {
        issues = append(issues, PreflightIssue{
            Severity: "warning",
            Message:  fmt.Sprintf("Cannot get OS info: %v", err),
            Node:     host,
        })
    } else {
        fmt.Printf("    %s: OS=%s/%s\n", host, osInfo.OS, osInfo.Arch)
    }

    // 3. Disk space check
    checkPath := d.topology.Global.DataDir
    if d.isLocal {
        // For local, check home directory
        homeDir, _ := os.UserHomeDir()
        checkPath = homeDir
    }

    available, err := exec.GetDiskSpace(checkPath)
    if err != nil {
        issues = append(issues, PreflightIssue{
            Severity: "warning",
            Message:  fmt.Sprintf("Cannot check disk space: %v", err),
            Node:     host,
        })
    } else {
        const minDiskSpace = 10 * 1024 * 1024 * 1024 // 10GB
        if available < minDiskSpace {
            issues = append(issues, PreflightIssue{
                Severity: "error",
                Message:  fmt.Sprintf("Insufficient disk space: %d GB available, %d GB required",
                    available/(1024*1024*1024), minDiskSpace/(1024*1024*1024)),
                Node:     host,
            })
        } else {
            fmt.Printf("    %s: Disk space OK (%d GB available)\n",
                host, available/(1024*1024*1024))
        }
    }

    // 4. Port availability check
    portsToCheck := d.getPortsForHost(host)
    for _, port := range portsToCheck {
        available, err := exec.CheckPortAvailable(port)
        if err != nil {
            issues = append(issues, PreflightIssue{
                Severity: "warning",
                Message:  fmt.Sprintf("Cannot check port %d: %v", port, err),
                Node:     fmt.Sprintf("%s:%d", host, port),
            })
        } else if !available {
            issues = append(issues, PreflightIssue{
                Severity: "error",
                Message:  fmt.Sprintf("Port %d already in use", port),
                Node:     fmt.Sprintf("%s:%d", host, port),
            })
        }
    }
    if len(portsToCheck) > 0 {
        fmt.Printf("    %s: Ports available: %v\n", host, portsToCheck)
    }

    // 5. User existence check (for remote deployment)
    if !d.isLocal {
        user := d.topology.Global.User
        exists, err := exec.UserExists(user)
        if err != nil {
            issues = append(issues, PreflightIssue{
                Severity: "warning",
                Message:  fmt.Sprintf("Cannot check user %s: %v", user, err),
                Node:     host,
            })
        } else if !exists {
            issues = append(issues, PreflightIssue{
                Severity: "warning",
                Message:  fmt.Sprintf("User %s does not exist, will be created", user),
                Node:     host,
            })
        }
    }

    return issues
}

// getPortsForHost collects all ports used by nodes on a specific host
func (d *Deployer) getPortsForHost(host string) []int {
    ports := []int{}

    for _, node := range d.topology.Mongod {
        if node.Host == host {
            ports = append(ports, node.Port)
        }
    }
    for _, node := range d.topology.Mongos {
        if node.Host == host {
            ports = append(ports, node.Port)
        }
    }
    for _, node := range d.topology.ConfigSvr {
        if node.Host == host {
            ports = append(ports, node.Port)
        }
    }

    return ports
}
```

**Key Advantages:**
- Single code path for both local and remote
- Parallel checking for all hosts
- Clear separation of errors vs warnings
- Easy to test with MockExecutor
- No duplicated logic

**Output:**
- Binary path stored in `d.binPath` for use in Phase 3
- Preflight issues reported (errors block deployment, warnings are informational)
- All checks pass before proceeding to Phase 3

**Implementation Notes:**

1. **Deployer Struct Update:**
   ```go
   // pkg/deploy/deployer.go
   type Deployer struct {
       clusterName string
       version     string
       topology    *topology.Topology
       executors   map[string]executor.Executor
       metaDir     string
       isLocal     bool
       binPath     string  // NEW: Path to MongoDB binaries (from binary manager)
   }
   ```

2. **Dependencies:**
   - Standalone implementation using MongoDB's full.json API
   - No additional dependencies needed

3. **Error Handling:**
   - Binary download failures are fatal (deployment cannot proceed)
   - Pre-flight errors are fatal (deployment cannot proceed)
   - Pre-flight warnings are informational (deployment can proceed)

4. **Performance:**
   - Binary downloads are cached in `~/.mup/storage/packages/`
   - Pre-flight checks run in parallel for all hosts
   - Subsequent deployments with same version skip download

---

### Phase 3: Deploy

**Package:** `pkg/deploy/`

This phase handles the actual file distribution and configuration generation.

#### 3.1 Port Allocation (Local Mode)

```go
type PortAllocator struct {
    basePort      int
    usedPorts     map[int]bool
    portRanges    map[ComponentType]int  // Offset for each component type
}

func (pa *PortAllocator) AllocatePorts(topology *Topology) error {
    // Reserve port ranges
    // mongod: basePort + 0-99
    // config servers: basePort + 100-199
    // mongos: basePort + 200-299

    offset := 0

    // Allocate for mongod servers
    for i, node := range topology.MongodServers {
        port := pa.findNextAvailable(pa.basePort + offset)
        node.Port = port
        offset++
    }

    // Allocate for config servers
    offset = 100
    for i, node := range topology.ConfigServers {
        port := pa.findNextAvailable(pa.basePort + offset)
        node.Port = port
        offset++
    }

    // Allocate for mongos
    offset = 200
    for i, node := range topology.MongosServers {
        port := pa.findNextAvailable(pa.basePort + offset)
        node.Port = port
        offset++
    }

    return nil
}
```

#### 3.2 Directory Structure Creation

**Remote Mode:**
```
/opt/mup/mongodb/<version>/          # Binary directory
  ├── bin/
  │   ├── mongod
  │   ├── mongos
  │   └── mongo
  └── lib/

/data/mongodb/                       # Data directory (per node)
  ├── db/
  └── mongod.pid

/var/log/mongodb/                    # Log directory
  └── mongod.log

/etc/mongodb/                        # Config directory
  └── mongod.conf
```

**Local Mode:**
```
~/.mup/local-clusters/<cluster-name>/
  ├── binaries/
  │   └── <version>/
  │       └── bin/
  ├── data/
  │   ├── node-30000/
  │   │   └── db/
  │   ├── node-30001/
  │   │   └── db/
  │   └── node-30002/
  │       └── db/
  ├── logs/
  │   ├── node-30000.log
  │   ├── node-30001.log
  │   └── node-30002.log
  └── configs/
      ├── node-30000.conf
      ├── node-30001.conf
      └── node-30002.conf
```

#### 3.3 File Distribution

**Using Unified Executor Interface:**

```go
type Deployer struct {
    executors map[string]Executor  // One executor per host
    topology  *Topology
}

func (d *Deployer) DeployBinaries(binaryPath string) error {
    // Extract tarball locally first
    tempDir, err := extractToTemp(binaryPath)
    if err != nil {
        return err
    }
    defer os.RemoveAll(tempDir)

    // Deploy to each unique host in parallel
    hosts := d.topology.UniqueHosts()

    var wg sync.WaitGroup
    errors := make(chan error, len(hosts))

    for _, host := range hosts {
        wg.Add(1)
        go func(h string) {
            defer wg.Done()

            executor := d.executors[h]
            targetDir := d.topology.DeployDirForHost(h)

            // Create directory - works for both local and remote
            if err := executor.CreateDirectory(targetDir, 0755); err != nil {
                errors <- fmt.Errorf("failed to create dir on %s: %w", h, err)
                return
            }

            // Upload files - works for both local and remote
            if err := d.uploadDirectory(executor, tempDir, targetDir); err != nil {
                errors <- fmt.Errorf("failed to upload to %s: %w", h, err)
                return
            }

            // Set permissions - works for both local and remote
            cmd := fmt.Sprintf("chmod +x %s/bin/*", targetDir)
            if _, err := executor.Execute(cmd); err != nil {
                errors <- fmt.Errorf("failed to set permissions on %s: %w", h, err)
                return
            }
        }(host)
    }

    wg.Wait()
    close(errors)

    // Check for errors
    if len(errors) > 0 {
        return <-errors
    }

    return nil
}

func (d *Deployer) uploadDirectory(executor Executor, localDir, remoteDir string) error {
    // Walk local directory and upload each file
    return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // Skip directories (created as needed)
        if info.IsDir() {
            return nil
        }

        // Calculate relative path
        relPath, _ := filepath.Rel(localDir, path)
        remotePath := filepath.Join(remoteDir, relPath)

        // Ensure remote directory exists
        remoteParent := filepath.Dir(remotePath)
        executor.CreateDirectory(remoteParent, 0755)

        // Upload file
        return executor.UploadFile(path, remotePath)
    })
}
```

**Benefits:**
- No mode-specific branching
- Same code path for local and remote
- Easy to test with MockExecutor
- Can mix local and remote nodes (advanced use case)

#### 3.4 Configuration Generation

```go
type ConfigGenerator struct {
    templates template.Manager
    mode      DeploymentMode
}

func (cg *ConfigGenerator) GenerateNodeConfig(node *Node, topology *Topology) (string, error) {
    tmpl := cg.templates.GetTemplate(node.MongoVersion, node.ComponentType)

    data := ConfigData{
        // Paths
        DataDir:    node.DataDir,
        LogDir:     node.LogDir,
        BinaryPath: node.BinaryPath(),

        // Network
        Port:   node.Port,
        BindIP: cg.determineBindIP(node),

        // Replication
        ReplicaSet:      node.ReplicaSet,
        ReplicaPriority: node.ReplicaPriority,
        Votes:           node.Votes,

        // Sharding (if applicable)
        ShardingRole: node.ShardingRole,
        ConfigServers: topology.ConfigServerConnectionString(),

        // Storage
        StorageEngine: topology.RuntimeConfig.Storage.Engine,
        CacheSizeGB:   topology.RuntimeConfig.Storage.WiredTiger.CacheSizeGB,

        // Security
        Auth:    topology.Security.AuthEnabled,
        KeyFile: node.KeyFile,
        TLS:     topology.Security.TLS,

        // Process Management
        Fork:        d.mode == ModeRemote,  // systemd for remote, no fork for local
        PIDFile:     node.PIDFile(),
    }

    return tmpl.Execute(data)
}

func (cg *ConfigGenerator) determineBindIP(node *Node) string {
    if cg.mode == ModeLocal {
        return "127.0.0.1"  // Local only
    }

    // Remote mode: bind to all interfaces or specific IP
    if node.BindIP != "" {
        return node.BindIP
    }
    return "0.0.0.0"  // Default: all interfaces
}
```

#### 3.5 Service File Generation (Remote Mode Only)

```go
func (d *Deployer) GenerateSystemdService(node *Node) (string, error) {
    tmpl := `[Unit]
Description=MongoDB Database Server ({{ .ReplicaSet }})
After=network.target

[Service]
Type=forking
User={{ .User }}
Group={{ .User }}
PIDFile={{ .PIDFile }}
ExecStart={{ .BinaryPath }} --config {{ .ConfigFile }}
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5
LimitNOFILE=64000
LimitNPROC=64000

[Install]
WantedBy=multi-user.target
`

    data := map[string]string{
        "ReplicaSet": node.ReplicaSet,
        "User":       node.User,
        "PIDFile":    node.PIDFile(),
        "BinaryPath": node.BinaryPath(),
        "ConfigFile": node.ConfigFile,
    }

    return executeTemplate(tmpl, data)
}
```

---

### Phase 4: Initialize

**Package:** `pkg/mongo/`, `pkg/deploy/initialize.go`

#### 4.1 Start Processes

**Local Mode:**
```go
func (i *Initializer) StartLocal(node *Node) error {
    cmd := exec.Command(
        node.BinaryPath(),
        "--config", node.ConfigFile,
    )

    // Set up logging
    logFile, _ := os.Create(node.LogPath())
    cmd.Stdout = logFile
    cmd.Stderr = logFile

    // Start in background
    if err := cmd.Start(); err != nil {
        return err
    }

    // Save PID for later management
    node.PID = cmd.Process.Pid

    // Wait for ready
    return i.waitForReady(node, 30*time.Second)
}
```

**Remote Mode:**
```go
func (i *Initializer) StartRemote(node *Node) error {
    // Upload service file
    serviceContent := i.generateSystemdService(node)
    servicePath := fmt.Sprintf("/etc/systemd/system/mongod-%s.service", node.ReplicaSet)

    i.sshExecutor.UploadContent(node.Host, serviceContent, servicePath)

    // Reload systemd
    i.sshExecutor.Execute(node.Host, "systemctl daemon-reload")

    // Enable and start service
    i.sshExecutor.Execute(node.Host, fmt.Sprintf("systemctl enable mongod-%s", node.ReplicaSet))
    i.sshExecutor.Execute(node.Host, fmt.Sprintf("systemctl start mongod-%s", node.ReplicaSet))

    // Wait for ready
    return i.waitForReady(node, 30*time.Second)
}
```

#### 4.2 Initialize Replica Set

```go
type MongoAdmin struct {
    client *mongo.Client
}

func (ma *MongoAdmin) InitiateReplicaSet(primary *Node, members []*Node) error {
    // Connect to primary node
    ctx := context.Background()
    client, err := ma.connect(primary)
    if err != nil {
        return err
    }
    defer client.Disconnect(ctx)

    // Build replica set config
    config := bson.D{
        {Key: "_id", Value: primary.ReplicaSet},
        {Key: "members", Value: ma.buildMemberArray(members)},
    }

    // Initiate replica set
    cmd := bson.D{{Key: "replSetInitiate", Value: config}}
    result := client.Database("admin").RunCommand(ctx, cmd)

    if result.Err() != nil {
        return fmt.Errorf("failed to initiate replica set: %w", result.Err())
    }

    // Wait for primary election
    return ma.waitForPrimary(primary, 60*time.Second)
}

func (ma *MongoAdmin) buildMemberArray(members []*Node) []bson.D {
    arr := []bson.D{}
    for i, node := range members {
        member := bson.D{
            {Key: "_id", Value: i},
            {Key: "host", Value: fmt.Sprintf("%s:%d", node.Host, node.Port)},
            {Key: "priority", Value: node.ReplicaPriority},
            {Key: "votes", Value: node.Votes},
        }
        arr = append(arr, member)
    }
    return arr
}
```

#### 4.3 Initialize Sharded Cluster (if applicable)

```go
func (ma *MongoAdmin) InitializeSharding(mongos *Node, shards []*Node) error {
    // Connect to mongos
    client, err := ma.connect(mongos)
    if err != nil {
        return err
    }
    defer client.Disconnect(context.Background())

    // Add each shard
    for _, shard := range shards {
        shardConnStr := ma.buildShardConnectionString(shard)

        cmd := bson.D{
            {Key: "addShard", Value: shardConnStr},
            {Key: "name", Value: shard.ShardName},
        }

        result := client.Database("admin").RunCommand(context.Background(), cmd)
        if result.Err() != nil {
            return fmt.Errorf("failed to add shard %s: %w", shard.ShardName, result.Err())
        }
    }

    return nil
}
```

#### 4.4 Create Admin User (if auth enabled)

```go
func (ma *MongoAdmin) CreateAdminUser(node *Node, username, password string) error {
    // Must be done before auth is enforced
    ctx := context.Background()
    client, err := ma.connect(node)
    if err != nil {
        return err
    }
    defer client.Disconnect(ctx)

    cmd := bson.D{
        {Key: "createUser", Value: username},
        {Key: "pwd", Value: password},
        {Key: "roles", Value: bson.A{
            bson.D{{Key: "role", Value: "root"}, {Key: "db", Value: "admin"}},
        }},
    }

    result := client.Database("admin").RunCommand(ctx, cmd)
    return result.Err()
}
```

---

### Phase 5: Finalize

**Package:** `pkg/meta/`

#### 5.1 Save Metadata

```go
type MetaManager struct {
    storageDir string  // ~/.mup/storage/clusters/
}

func (mm *MetaManager) SaveClusterMeta(cluster *ClusterMeta) error {
    clusterDir := filepath.Join(mm.storageDir, cluster.ClusterName)

    // Create cluster directory
    if err := os.MkdirAll(clusterDir, 0755); err != nil {
        return err
    }

    // Save original topology
    topologyPath := filepath.Join(clusterDir, "topology.yaml")
    if err := mm.writeYAML(topologyPath, cluster.Topology); err != nil {
        return err
    }

    // Save cluster metadata
    metaPath := filepath.Join(clusterDir, "meta.yaml")

    // Add deployment timestamp
    cluster.DeployTimestamp = time.Now()
    cluster.LastModified = time.Now()

    // Write atomically (temp file + rename)
    tmpPath := metaPath + ".tmp"
    if err := mm.writeYAML(tmpPath, cluster); err != nil {
        return err
    }

    return os.Rename(tmpPath, metaPath)
}
```

#### 5.2 Generate Connection Info

```go
func (mm *MetaManager) GenerateConnectionInfo(cluster *ClusterMeta) *ConnectionInfo {
    info := &ConnectionInfo{
        ClusterName: cluster.ClusterName,
        Version:     cluster.Version,
    }

    if len(cluster.Topology.MongosServers) > 0 {
        // Sharded cluster: connect via mongos
        hosts := []string{}
        for _, mongos := range cluster.Topology.MongosServers {
            hosts = append(hosts, fmt.Sprintf("%s:%d", mongos.Host, mongos.Port))
        }
        info.ConnectionString = fmt.Sprintf("mongodb://%s/", strings.Join(hosts, ","))
        info.Type = "sharded"
    } else {
        // Replica set: connect to all members
        hosts := []string{}
        for _, node := range cluster.Topology.MongodServers {
            hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
        }
        rsName := cluster.Topology.MongodServers[0].ReplicaSet
        info.ConnectionString = fmt.Sprintf("mongodb://%s/?replicaSet=%s",
            strings.Join(hosts, ","), rsName)
        info.Type = "replica_set"
    }

    return info
}
```

#### 5.3 Display Summary

```go
func DisplayDeploymentSummary(cluster *ClusterMeta, info *ConnectionInfo) {
    fmt.Println("\n✓ Cluster deployed successfully!")
    fmt.Println("\n" + strings.Repeat("=", 60))
    fmt.Printf("Cluster Name:    %s\n", cluster.ClusterName)
    fmt.Printf("MongoDB Version: %s\n", cluster.Version)
    fmt.Printf("Cluster Type:    %s\n", info.Type)
    fmt.Printf("Nodes:           %d\n", len(cluster.Topology.AllNodes()))
    fmt.Println(strings.Repeat("=", 60))

    fmt.Println("\nConnection String:")
    fmt.Printf("  %s\n", info.ConnectionString)

    fmt.Println("\nNodes:")
    for _, node := range cluster.Topology.MongodServers {
        fmt.Printf("  - %s:%d (%s)\n", node.Host, node.Port, node.ReplicaSet)
    }

    fmt.Println("\nNext Steps:")
    fmt.Printf("  mup cluster display %s    # View cluster status\n", cluster.ClusterName)
    fmt.Printf("  mup cluster start %s      # Start cluster (if not auto-started)\n", cluster.ClusterName)

    if info.Type == "replica_set" {
        fmt.Printf("  mongo '%s'  # Connect to cluster\n", info.ConnectionString)
    } else {
        fmt.Printf("  mongo '%s'  # Connect via mongos\n", info.ConnectionString)
    }

    fmt.Println()
}
```

---

## Error Handling & Rollback

### Rollback Manager

```go
type RollbackManager struct {
    steps []RollbackStep
}

type RollbackStep struct {
    Phase       string
    Description string
    Action      func() error
}

func (rm *RollbackManager) AddStep(phase, desc string, action func() error) {
    rm.steps = append(rm.steps, RollbackStep{
        Phase:       phase,
        Description: desc,
        Action:      action,
    })
}

func (rm *RollbackManager) Execute() error {
    fmt.Println("\nRolling back deployment...")

    // Execute in reverse order
    for i := len(rm.steps) - 1; i >= 0; i-- {
        step := rm.steps[i]
        fmt.Printf("  - %s: %s\n", step.Phase, step.Description)

        if err := step.Action(); err != nil {
            fmt.Printf("    Warning: rollback step failed: %v\n", err)
            // Continue with other rollback steps
        }
    }

    return nil
}
```

### Usage in Deploy

```go
func (d *Deployer) Deploy(topology *Topology) error {
    rollback := &RollbackManager{}

    // Phase 3: Deploy files
    if err := d.deployBinaries(binaryPath); err != nil {
        rollback.Execute()
        return err
    }
    rollback.AddStep("Deploy", "Remove deployed binaries", func() error {
        return d.cleanupBinaries()
    })

    // Phase 4: Start services
    for _, node := range topology.AllNodes() {
        if err := d.startNode(node); err != nil {
            rollback.Execute()
            return err
        }
        rollback.AddStep("Initialize", fmt.Sprintf("Stop node %s", node.Host), func() error {
            return d.stopNode(node)
        })
    }

    // Success - clear rollback steps
    rollback.steps = nil
    return nil
}
```

---

## CLI Implementation

### Command Structure

```go
// cmd/mup/cluster.go

var clusterCmd = &cobra.Command{
    Use:   "cluster",
    Short: "Manage MongoDB clusters",
}

var deployCmd = &cobra.Command{
    Use:   "deploy <cluster-name> <version> <topology-file>",
    Short: "Deploy a new MongoDB cluster",
    Long: `Deploy a new MongoDB cluster from a topology file.

Supports both remote deployment (to VMs) and local deployment (single machine):
  - Remote: Each node has unique host, uses standard ports (27017)
  - Local:  All nodes on localhost, uses auto-allocated sequential ports

The deployment mode is automatically detected from the topology file.`,
    Args: cobra.ExactArgs(3),
    RunE: runDeploy,
}

func runDeploy(cmd *cobra.Command, args []string) error {
    clusterName := args[0]
    version := args[1]
    topologyFile := args[2]

    // Get flags
    user, _ := cmd.Flags().GetString("user")
    identityFile, _ := cmd.Flags().GetString("identity-file")
    skipConfirm, _ := cmd.Flags().GetBool("yes")
    localMode, _ := cmd.Flags().GetBool("local")
    portBase, _ := cmd.Flags().GetInt("port-base")

    // Create deployer
    deployer := deploy.NewDeployer(deploy.Config{
        ClusterName:  clusterName,
        Version:      version,
        TopologyFile: topologyFile,
        User:         user,
        SSHIdentity:  identityFile,
        ForceLocal:   localMode,
        PortBase:     portBase,
    })

    // Execute deployment phases
    fmt.Println("Starting deployment...")

    // Phase 1: Parse & Validate
    fmt.Println("\n[1/5] Parsing and validating topology...")
    topology, err := deployer.ParseAndValidate()
    if err != nil {
        return err
    }

    // Phase 2: Prepare
    fmt.Println("\n[2/5] Preparing deployment...")
    if err := deployer.Prepare(topology); err != nil {
        return err
    }

    // Show deployment plan
    deployer.DisplayPlan(topology)

    if !skipConfirm {
        if !promptConfirm("Continue with deployment?") {
            return fmt.Errorf("deployment cancelled")
        }
    }

    // Phase 3: Deploy
    fmt.Println("\n[3/5] Deploying files and configurations...")
    if err := deployer.Deploy(topology); err != nil {
        return err
    }

    // Phase 4: Initialize
    fmt.Println("\n[4/5] Initializing cluster...")
    if err := deployer.Initialize(topology); err != nil {
        return err
    }

    // Phase 5: Finalize
    fmt.Println("\n[5/5] Finalizing deployment...")
    if err := deployer.Finalize(topology); err != nil {
        return err
    }

    return nil
}

func init() {
    deployCmd.Flags().String("user", "mongodb", "SSH user for remote deployment")
    deployCmd.Flags().String("identity-file", "", "SSH private key file")
    deployCmd.Flags().Bool("yes", false, "Skip confirmation prompts")
    deployCmd.Flags().Bool("local", false, "Force local deployment mode")
    deployCmd.Flags().Int("port-base", 30000, "Base port for local deployment")

    clusterCmd.AddCommand(deployCmd)
    rootCmd.AddCommand(clusterCmd)
}
```

---

## Testing Strategy

### Unit Tests
- Port allocator logic
- Configuration template rendering
- Topology parsing and validation
- Deployment mode detection

### Integration Tests
- Local deployment (full workflow)
- Mock SSH operations for remote deployment testing
- Rollback scenarios
- Error handling

### End-to-End Tests
- Deploy local 3-node replica set
- Deploy to remote VMs (if available)
- Verify cluster initialization
- Connection string validation

---

## Next Steps

1. Implement Phase 1 (Parse & Validate)
   - `pkg/topology/parser.go`
   - `pkg/topology/validator.go`
   - `pkg/topology/types.go`

2. Implement Phase 2 (Prepare)
   - `pkg/repository/downloader.go`
   - `pkg/deploy/preflight.go`

3. Implement Phase 3 (Deploy)
   - `pkg/deploy/deployer.go`
   - `pkg/template/generator.go`

4. Implement Phase 4 (Initialize)
   - `pkg/mongo/admin.go`
   - `pkg/mongo/replicaset.go`

5. Implement Phase 5 (Finalize)
   - `pkg/meta/manager.go`

6. Add CLI command
   - `cmd/mup/cluster.go`
   - `cmd/mup/cluster_deploy.go`
