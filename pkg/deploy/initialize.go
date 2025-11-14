package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/zph/mup/pkg/logger"
)

// initialize implements Phase 4: Initialize
// - Wait for processes to be ready
// - Initialize replica sets
// - Start mongos (for sharded clusters)
// - Configure sharding (if applicable)
func (d *Deployer) initialize(ctx context.Context) error {
	fmt.Println("Phase 4: Initialize")
	fmt.Println("===================")

	// Step 1: Wait for processes to be ready (mongod and config servers only)
	if err := d.waitForProcesses(ctx); err != nil {
		return fmt.Errorf("failed waiting for processes: %w", err)
	}

	// Step 2: Initialize replica sets
	if err := d.initializeReplicaSets(ctx); err != nil {
		return fmt.Errorf("failed to initialize replica sets: %w", err)
	}

	// Step 3: For sharded clusters, configure and start mongos AFTER config servers are initialized
	if d.topology.GetTopologyType() == "sharded" {
		// Wait for config RS to stabilize before starting mongos
		fmt.Println("Waiting for config server replica set to stabilize...")
		time.Sleep(5 * time.Second)

		// Generate mongos configs now that config servers are initialized
		if err := d.generateMongosConfigs(ctx); err != nil {
			return fmt.Errorf("failed to generate mongos configs: %w", err)
		}

		// Start mongos processes
		if err := d.startMongosProcesses(ctx); err != nil {
			return fmt.Errorf("failed to start mongos processes: %w", err)
		}

		// Wait for mongos to be ready (give more time - mongos needs to connect to config RS)
		if err := d.waitForMongosProcesses(ctx); err != nil {
			return fmt.Errorf("failed waiting for mongos processes: %w", err)
		}

		// Configure sharding
		if err := d.configureSharding(ctx); err != nil {
			return fmt.Errorf("failed to configure sharding: %w", err)
		}
	}

	fmt.Println("✓ Phase 4 complete: Cluster initialized")
	return nil
}

// waitForProcesses waits for mongod and config server processes to be ready
// NOTE: Does not wait for mongos - those are started later after replica set init
func (d *Deployer) waitForProcesses(ctx context.Context) error {
	fmt.Println("Waiting for MongoDB processes to be ready...")

	maxWait := 2 * time.Minute
	pollInterval := 2 * time.Second
	deadline := time.Now().Add(maxWait)

	// Wait for config servers
	for _, node := range d.topology.ConfigSvr {
		if err := d.waitForNode(ctx, node.Host, node.Port, deadline, pollInterval); err != nil {
			return fmt.Errorf("config server %s:%d not ready: %w", node.Host, node.Port, err)
		}
	}

	// Wait for mongod nodes
	for _, node := range d.topology.Mongod {
		if err := d.waitForNode(ctx, node.Host, node.Port, deadline, pollInterval); err != nil {
			return fmt.Errorf("mongod %s:%d not ready: %w", node.Host, node.Port, err)
		}
	}

	// NOTE: mongos nodes are NOT waited for here - they're started after replica set init

	fmt.Println("  ✓ All processes ready")
	return nil
}

// waitForNode waits for a specific MongoDB node to be ready
func (d *Deployer) waitForNode(ctx context.Context, host string, port int, deadline time.Time, pollInterval time.Duration) error {
	exec := d.executors[host]
	attempt := 0

	logger.Debug("Waiting for node %s:%d to be ready", host, port)

	for time.Now().Before(deadline) {
		attempt++
		logger.Debug("  Attempt %d: checking port %s:%d", attempt, host, port)

		// Check if port is listening
		available, err := exec.CheckPortAvailable(port)
		logger.Debug("  Port %s:%d available=%v, err=%v", host, port, available, err)

		if err == nil && !available {
			// Port is in use (process is listening) - node is ready
			fmt.Printf("  ✓ Node %s:%d ready\n", host, port)
			logger.Debug("Node %s:%d is ready after %d attempts", host, port, attempt)
			return nil
		}

		if err != nil {
			logger.Warn("Error checking port %s:%d: %v", host, port, err)
		}

		// Wait before next poll
		logger.Debug("  Sleeping %v before next attempt", pollInterval)
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for node to be ready")
}

// initializeReplicaSets initializes all replica sets in the topology in parallel
func (d *Deployer) initializeReplicaSets(ctx context.Context) error {
	// Collect replica sets
	replicaSets := d.collectReplicaSets()

	if len(replicaSets) == 0 {
		fmt.Println("No replica sets to initialize")
		return nil
	}

	fmt.Printf("Initializing %d replica set(s) in parallel...\n", len(replicaSets))

	// Use channels to collect errors from parallel initialization
	type result struct {
		rsName string
		err    error
	}
	results := make(chan result, len(replicaSets))

	// Start initialization for all replica sets in parallel
	for rsName, members := range replicaSets {
		go func(name string, mems []ReplicaSetMember) {
			err := d.initializeReplicaSet(ctx, name, mems)
			results <- result{rsName: name, err: err}
		}(rsName, members)
	}

	// Wait for all to complete and collect any errors
	var firstError error
	for i := 0; i < len(replicaSets); i++ {
		res := <-results
		if res.err != nil && firstError == nil {
			firstError = fmt.Errorf("failed to initialize replica set %s: %w", res.rsName, res.err)
		}
	}

	if firstError != nil {
		return firstError
	}

	return nil
}

// ReplicaSetMember represents a member of a replica set
type ReplicaSetMember struct {
	Host     string
	Port     int
	Priority float64
	Hidden   bool
	Votes    int
}

// collectReplicaSets collects all replica sets from the topology
func (d *Deployer) collectReplicaSets() map[string][]ReplicaSetMember {
	replicaSets := make(map[string][]ReplicaSetMember)

	// Collect from mongod nodes
	for _, node := range d.topology.Mongod {
		if node.ReplicaSet != "" {
			member := ReplicaSetMember{
				Host:     node.Host,
				Port:     node.Port,
				Priority: 1.0,
				Hidden:   false,
				Votes:    1,
			}

			if node.Priority != nil {
				member.Priority = *node.Priority
			}
			if node.Hidden != nil {
				member.Hidden = *node.Hidden
			}
			if node.Votes != nil {
				member.Votes = *node.Votes
			}

			replicaSets[node.ReplicaSet] = append(replicaSets[node.ReplicaSet], member)
		}
	}

	// Collect from config servers
	for _, node := range d.topology.ConfigSvr {
		member := ReplicaSetMember{
			Host:     node.Host,
			Port:     node.Port,
			Priority: 1.0,
			Hidden:   false,
			Votes:    1,
		}
		replicaSets[node.ReplicaSet] = append(replicaSets[node.ReplicaSet], member)
	}

	return replicaSets
}

// initializeReplicaSet initializes a single replica set using MongoDB driver
func (d *Deployer) initializeReplicaSet(ctx context.Context, rsName string, members []ReplicaSetMember) error {
	if len(members) == 0 {
		return fmt.Errorf("replica set %s has no members", rsName)
	}

	fmt.Printf("  Initializing replica set: %s\n", rsName)

	// Use first member as primary for initialization
	primary := members[0]
	primaryHost := fmt.Sprintf("%s:%d", primary.Host, primary.Port)
	connStr := fmt.Sprintf("mongodb://%s", primaryHost)

	// Create context with timeout for initial connection
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Connect directly to the primary node (use SetDirect to avoid replica set discovery)
	client, err := mongo.Connect(initCtx, options.Client().
		ApplyURI(connStr).
		SetDirect(true).
		SetServerSelectionTimeout(10*time.Second).
		SetConnectTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("failed to connect to primary node %s: %w", primaryHost, err)
	}
	defer func() {
		if err := client.Disconnect(initCtx); err != nil {
			// Ignore disconnect errors during cleanup
		}
	}()

	// Check if replica set is already initialized
	var status bson.M
	err = client.Database("admin").RunCommand(initCtx, bson.M{"replSetGetStatus": 1}).Decode(&status)
	if err == nil {
		fmt.Printf("    ✓ Replica set %s already initialized\n", rsName)
		return nil
	}

	// Build members list for replica set configuration
	memberDocs := make([]bson.M, len(members))
	for i, member := range members {
		memberDoc := bson.M{
			"_id":  i,
			"host": fmt.Sprintf("%s:%d", member.Host, member.Port),
		}

		// Only include optional fields if they differ from defaults
		if member.Priority != 1.0 {
			memberDoc["priority"] = member.Priority
		}
		if member.Hidden {
			memberDoc["hidden"] = member.Hidden
		}
		if member.Votes != 1 {
			memberDoc["votes"] = member.Votes
		}

		memberDocs[i] = memberDoc
	}

	// Initialize replica set using replSetInitiate command
	cmd := bson.D{
		{Key: "replSetInitiate", Value: bson.M{
			"_id":     rsName,
			"version": 1,
			"members": memberDocs,
		}},
	}

	err = client.Database("admin").RunCommand(initCtx, cmd).Err()
	if err != nil {
		// Check if it's already initialized
		errStr := err.Error()
		if strings.Contains(errStr, "already initialized") ||
			strings.Contains(errStr, "already has") ||
			strings.Contains(errStr, "already been initiated") {
			fmt.Printf("    ✓ Replica set %s already initialized\n", rsName)
			return nil
		}
		// Skip RSGhost errors - these are transient and will be handled by the wait loop
		if strings.Contains(errStr, "RSGhost") || strings.Contains(errStr, "server selection error") {
			fmt.Printf("    Replica set %s initialization returned RSGhost error (will wait for topology to stabilize)\n", rsName)
			// Continue to wait loop - don't fail yet
		} else {
			return fmt.Errorf("failed to initialize replica set: %w", err)
		}
	}

	// Disconnect and wait for replica set to initialize
	client.Disconnect(initCtx)

	// Wait for primary to be elected by polling replSetGetStatus
	fmt.Printf("    Waiting for primary to be elected in replica set %s...\n", rsName)

	// Create a new context with longer timeout for waiting
	waitCtx, waitCancel := context.WithTimeout(ctx, 120*time.Second)
	defer waitCancel()

	maxRetries := 60
	retryInterval := 2 * time.Second

	// First, wait a bit for replSetInitiate to propagate
	time.Sleep(3 * time.Second)

	for i := 0; i < maxRetries; i++ {
		// Check if context is cancelled
		if waitCtx.Err() != nil {
			return fmt.Errorf("context cancelled while waiting for primary: %w", waitCtx.Err())
		}

		// Wait before checking
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timeout waiting for primary election in replica set %s", rsName)
		case <-time.After(retryInterval):
		}

		// Try to connect directly to first host to check status
		checkConnStr := fmt.Sprintf("mongodb://%s", primaryHost)
		checkClient, err := mongo.Connect(waitCtx, options.Client().
			ApplyURI(checkConnStr).
			SetDirect(true).
			SetServerSelectionTimeout(5*time.Second).
			SetConnectTimeout(5*time.Second))
		if err != nil {
			fmt.Printf("    Waiting for replica set to initialize... (attempt %d/%d)\n", i+1, maxRetries)
			continue
		}

		// Check replica set status
		var status bson.M
		err = checkClient.Database("admin").RunCommand(waitCtx, bson.M{"replSetGetStatus": 1}).Decode(&status)
		checkClient.Disconnect(waitCtx)

		if err != nil {
			fmt.Printf("    Waiting for replica set to initialize... (attempt %d/%d)\n", i+1, maxRetries)
			continue
		}

		// Check if we have a primary and all members are in valid states
		if statusMembers, ok := status["members"].(bson.A); ok {
			hasPrimary := false
			allValid := true
			allReady := true
			validStates := map[string]bool{
				"PRIMARY":   true,
				"SECONDARY": true,
				"ARBITER":   true,
			}
			readyStates := map[string]bool{
				"PRIMARY":   true,
				"SECONDARY": true,
			}

			for _, m := range statusMembers {
				if member, ok := m.(bson.M); ok {
					stateStr, _ := member["stateStr"].(string)
					if stateStr == "PRIMARY" {
						hasPrimary = true
					}
					// Check if member is in an invalid state (STARTUP, STARTUP2, RSGhost, etc.)
					if !validStates[stateStr] && stateStr != "" {
						allValid = false
						fmt.Printf("    Member %v is in state %s (attempt %d/%d)\n", member["name"], stateStr, i+1, maxRetries)
					}
					// Check if member is in a ready state (PRIMARY or SECONDARY, not ARBITER)
					// ARBITER is valid but not "ready" for data operations
					if !readyStates[stateStr] && stateStr != "" {
						allReady = false
					}
				}
			}

			// Only proceed if we have a primary, all members are valid, and all data members are ready
			if hasPrimary && allValid && allReady {
				// Wait a bit more for topology to stabilize
				time.Sleep(3 * time.Second)

				// Now verify that we can actually connect using the replica set connection string
				// This ensures the replica set topology is ready (not RSGhost)
				var hosts []string
				for _, member := range members {
					hosts = append(hosts, fmt.Sprintf("%s:%d", member.Host, member.Port))
				}
				replSetConnStr := fmt.Sprintf("mongodb://%s/?replicaSet=%s", strings.Join(hosts, ","), rsName)
				connectionVerified := false
				lastError := error(nil)

				for verifyAttempt := 0; verifyAttempt < 5; verifyAttempt++ {
					replSetClient, err := mongo.Connect(waitCtx, options.Client().
						ApplyURI(replSetConnStr).
						SetServerSelectionTimeout(10*time.Second))
					if err == nil {
						// Try to ping to verify connection works
						pingCtx, pingCancel := context.WithTimeout(waitCtx, 10*time.Second)
						err = replSetClient.Ping(pingCtx, nil)
						pingCancel()
						replSetClient.Disconnect(waitCtx)

						if err == nil {
							connectionVerified = true
							break
						}
						lastError = err
					} else {
						lastError = err
					}

					// Wait before retrying (longer wait for later attempts)
					if verifyAttempt < 4 {
						waitTime := 2 * time.Second
						if verifyAttempt >= 2 {
							waitTime = 3 * time.Second
						}
						time.Sleep(waitTime)
					}
				}

				if connectionVerified {
					fmt.Printf("    ✓ Primary elected and all members ready in replica set %s\n", rsName)
					return nil
				}

				// Only log error if we're getting close to timeout
				if i >= maxRetries-5 {
					fmt.Printf("    Replica set connection not ready yet (attempt %d/%d): %v\n", i+1, maxRetries, lastError)
				}
				// Continue outer loop to retry - don't fail yet
			}

			if !hasPrimary {
				fmt.Printf("    Waiting for primary election... (attempt %d/%d)\n", i+1, maxRetries)
			} else if !allValid {
				fmt.Printf("    Waiting for all members to be ready... (attempt %d/%d)\n", i+1, maxRetries)
			} else if !allReady {
				fmt.Printf("    Waiting for all data members to be ready (PRIMARY/SECONDARY)... (attempt %d/%d)\n", i+1, maxRetries)
			}
		}
	}

	return fmt.Errorf("timeout waiting for primary to be elected in replica set %s", rsName)
}

// configureSharding configures sharding for a sharded cluster
func (d *Deployer) configureSharding(ctx context.Context) error {
	if len(d.topology.Mongos) == 0 {
		return fmt.Errorf("no mongos nodes found for sharded cluster")
	}

	fmt.Println("Configuring sharding...")

	// Use first mongos for configuration
	mongos := d.topology.Mongos[0]
	mongosHost := fmt.Sprintf("%s:%d", mongos.Host, mongos.Port)
	connStr := fmt.Sprintf("mongodb://%s", mongosHost)

	// Create context with timeout for connection
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Connect to mongos
	client, err := mongo.Connect(initCtx, options.Client().
		ApplyURI(connStr).
		SetServerSelectionTimeout(10*time.Second).
		SetConnectTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("failed to connect to mongos at %s: %w", mongosHost, err)
	}
	defer func() {
		if err := client.Disconnect(initCtx); err != nil {
			// Ignore disconnect errors during cleanup
		}
	}()

	// Add each replica set as a shard
	replicaSets := d.collectReplicaSets()

	for rsName, members := range replicaSets {
		// Skip config server replica set
		isConfigRS := false
		for _, cs := range d.topology.ConfigSvr {
			if cs.ReplicaSet == rsName {
				isConfigRS = true
				break
			}
		}
		if isConfigRS {
			continue
		}

		// Check if shard already exists
		var shards bson.M
		err = client.Database("admin").RunCommand(initCtx, bson.M{"listShards": 1}).Decode(&shards)
		if err == nil {
			if shardList, ok := shards["shards"].(bson.A); ok {
				shardExists := false
				for _, s := range shardList {
					if sMap, ok := s.(bson.M); ok {
						if id, ok := sMap["_id"].(string); ok && id == rsName {
							fmt.Printf("  ✓ Shard %s already added\n", rsName)
							shardExists = true
							break
						}
					}
				}
				if shardExists {
					continue
				}
			}
		}

		// Build connection string for this shard
		// Format: {replicaSetName}/{host1,host2,host3}
		var hosts []string
		for _, member := range members {
			hosts = append(hosts, fmt.Sprintf("%s:%d", member.Host, member.Port))
		}
		shardConnStr := fmt.Sprintf("%s/%s", rsName, strings.Join(hosts, ","))

		// Add shard using addShard command
		addShardCmd := bson.M{
			"addShard": shardConnStr,
		}

		err = client.Database("admin").RunCommand(initCtx, addShardCmd).Err()
		if err != nil {
			// Check if shard already exists (might have been added between check and add)
			if strings.Contains(err.Error(), "already exists") {
				fmt.Printf("  ✓ Shard %s already added\n", rsName)
				continue
			}
			return fmt.Errorf("failed to add shard %s: %w", rsName, err)
		}

		fmt.Printf("  ✓ Added shard: %s\n", rsName)
	}

	return nil
}

// generateMongosConfigs generates mongos configuration files
// This is called after config servers are initialized so configDB points to initialized RS
func (d *Deployer) generateMongosConfigs(ctx context.Context) error {
	if len(d.topology.Mongos) == 0 {
		return nil
	}

	fmt.Println("Generating mongos configuration files...")

	for _, node := range d.topology.Mongos {
		if err := d.generateMongosConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for mongos %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	return nil
}

// startMongosProcesses starts all mongos processes via supervisor
// This is called after config servers are initialized as a replica set
func (d *Deployer) startMongosProcesses(ctx context.Context) error {
	if len(d.topology.Mongos) == 0 {
		return nil
	}

	fmt.Println("Starting mongos processes via supervisor...")

	// Final port verification before starting mongos
	for _, node := range d.topology.Mongos {
		exec := d.executors[node.Host]
		available, err := exec.CheckPortAvailable(node.Port)
		if err != nil {
			return fmt.Errorf("failed to verify mongos port %s:%d: %w", node.Host, node.Port, err)
		}
		if !available {
			return fmt.Errorf("mongos port %s:%d is no longer available", node.Host, node.Port)
		}
	}

	// Start mongos processes via supervisor
	for _, node := range d.topology.Mongos {
		programName := fmt.Sprintf("mongos-%d", node.Port)
		fmt.Printf("  Starting mongos %s:%d via supervisor (program: %s)\n",
			node.Host, node.Port, programName)
		if err := d.supervisorMgr.StartProcess(programName); err != nil {
			return fmt.Errorf("failed to start mongos %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	return nil
}

// waitForMongosProcesses waits for all mongos processes to be ready
// This includes both port binding AND successful connection to config servers
func (d *Deployer) waitForMongosProcesses(ctx context.Context) error {
	if len(d.topology.Mongos) == 0 {
		return nil
	}

	fmt.Println("Waiting for mongos processes to be ready...")

	// Give mongos more time - it needs to connect to config servers which may take a while
	maxWait := 3 * time.Minute
	pollInterval := 2 * time.Second
	deadline := time.Now().Add(maxWait)

	// First, wait for ports to be listening
	for _, node := range d.topology.Mongos {
		if err := d.waitForNode(ctx, node.Host, node.Port, deadline, pollInterval); err != nil {
			return fmt.Errorf("mongos %s:%d port not ready: %w", node.Host, node.Port, err)
		}
	}

	// Then, verify mongos can connect to config servers by attempting a ping
	fmt.Println("  Verifying mongos connection to config servers...")
	for _, node := range d.topology.Mongos {
		if err := d.waitForMongosHealth(ctx, node.Host, node.Port, deadline); err != nil {
			return fmt.Errorf("mongos %s:%d not healthy: %w", node.Host, node.Port, err)
		}
	}

	fmt.Println("  ✓ All mongos processes ready")
	return nil
}

// waitForMongosHealth waits for mongos to successfully connect to config servers
func (d *Deployer) waitForMongosHealth(ctx context.Context, host string, port int, deadline time.Time) error {
	mongosHost := fmt.Sprintf("%s:%d", host, port)
	connStr := fmt.Sprintf("mongodb://%s", mongosHost)

	for time.Now().Before(deadline) {
		// Try to connect and ping
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		client, err := mongo.Connect(checkCtx, options.Client().
			ApplyURI(connStr).
			SetServerSelectionTimeout(5*time.Second).
			SetConnectTimeout(5*time.Second))

		if err == nil {
			// Try to ping to verify mongos is fully functional
			pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
			err = client.Ping(pingCtx, nil)
			pingCancel()
			client.Disconnect(checkCtx)

			if err == nil {
				cancel()
				fmt.Printf("  ✓ Mongos %s healthy\n", mongosHost)
				return nil
			}
		}

		cancel()

		// Wait before retry
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for mongos health")
		case <-time.After(2 * time.Second):
		}
	}

	return fmt.Errorf("timeout waiting for mongos to connect to config servers")
}
