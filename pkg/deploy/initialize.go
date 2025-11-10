package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// initialize implements Phase 4: Initialize
// - Wait for processes to be ready
// - Initialize replica sets
// - Configure sharding (if applicable)
func (d *Deployer) initialize(ctx context.Context) error {
	fmt.Println("Phase 4: Initialize")
	fmt.Println("===================")

	// Step 1: Wait for processes to be ready
	if err := d.waitForProcesses(ctx); err != nil {
		return fmt.Errorf("failed waiting for processes: %w", err)
	}

	// Step 2: Initialize replica sets
	if err := d.initializeReplicaSets(ctx); err != nil {
		return fmt.Errorf("failed to initialize replica sets: %w", err)
	}

	// Step 3: Configure sharding if this is a sharded cluster
	if d.topology.GetTopologyType() == "sharded" {
		if err := d.configureSharding(ctx); err != nil {
			return fmt.Errorf("failed to configure sharding: %w", err)
		}
	}

	fmt.Println("✓ Phase 4 complete: Cluster initialized\n")
	return nil
}

// waitForProcesses waits for all MongoDB processes to be ready
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

	// Wait for mongos nodes
	for _, node := range d.topology.Mongos {
		if err := d.waitForNode(ctx, node.Host, node.Port, deadline, pollInterval); err != nil {
			return fmt.Errorf("mongos %s:%d not ready: %w", node.Host, node.Port, err)
		}
	}

	fmt.Println("  ✓ All processes ready")
	return nil
}

// waitForNode waits for a specific MongoDB node to be ready
func (d *Deployer) waitForNode(ctx context.Context, host string, port int, deadline time.Time, pollInterval time.Duration) error {
	exec := d.executors[host]

	for time.Now().Before(deadline) {
		// Check if port is listening
		available, err := exec.CheckPortAvailable(port)
		if err == nil && !available {
			// Port is in use (process is listening) - node is ready
			fmt.Printf("  ✓ Node %s:%d ready\n", host, port)
			return nil
		}

		// Wait before next poll
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for node to be ready")
}

// initializeReplicaSets initializes all replica sets in the topology
func (d *Deployer) initializeReplicaSets(ctx context.Context) error {
	// Collect replica sets
	replicaSets := d.collectReplicaSets()

	if len(replicaSets) == 0 {
		fmt.Println("No replica sets to initialize")
		return nil
	}

	fmt.Printf("Initializing %d replica set(s)...\n", len(replicaSets))

	for rsName, members := range replicaSets {
		if err := d.initializeReplicaSet(ctx, rsName, members); err != nil {
			return fmt.Errorf("failed to initialize replica set %s: %w", rsName, err)
		}
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
	exec := d.executors[mongos.Host]

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

		// Build connection string for this shard
		var hosts []string
		for _, member := range members {
			hosts = append(hosts, fmt.Sprintf("%s:%d", member.Host, member.Port))
		}
		shardConnStr := fmt.Sprintf("%s/%s", rsName, joinStrings(hosts, ","))

		// Add shard
		mongoCommand := fmt.Sprintf(`mongosh --port %d --quiet --eval 'sh.addShard("%s")'`,
			mongos.Port, shardConnStr)

		output, err := exec.Execute(mongoCommand)
		if err != nil {
			return fmt.Errorf("failed to add shard %s: %w\nOutput: %s", rsName, err, output)
		}

		fmt.Printf("  ✓ Added shard: %s\n", rsName)
	}

	return nil
}
