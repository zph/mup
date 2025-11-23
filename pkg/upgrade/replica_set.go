package upgrade

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ReplicaSetMember represents a member of a replica set
type ReplicaSetMember struct {
	Host      string
	Port      int
	StateStr  string // PRIMARY, SECONDARY, ARBITER, etc.
	StateNum  int32
	Health    int32
	IsPrimary bool
}

// GetReplicaSetStatus gets the current replica set status
func GetReplicaSetStatus(ctx context.Context, rsName string, hosts []string) ([]ReplicaSetMember, error) {
	// Connect to replica set
	var hostsStr []string
	for _, h := range hosts {
		hostsStr = append(hostsStr, h)
	}
	uri := fmt.Sprintf("mongodb://%s/?replicaSet=%s", strings.Join(hostsStr, ","), rsName)

	clientOpts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to replica set: %w", err)
	}
	defer client.Disconnect(ctx)

	// Run replSetGetStatus
	adminDB := client.Database("admin")
	var result bson.M
	err = adminDB.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica set status: %w", err)
	}

	members, ok := result["members"].(bson.A)
	if !ok {
		return nil, fmt.Errorf("unexpected replica set status format")
	}

	var rsmembers []ReplicaSetMember
	for _, memberInterface := range members {
		member, ok := memberInterface.(bson.M)
		if !ok {
			continue
		}

		name, _ := member["name"].(string)
		state, _ := member["state"].(int32)
		stateStr, _ := member["stateStr"].(string)
		health, _ := member["health"].(int32)

		// Parse host:port
		parts := strings.Split(name, ":")
		var host string
		var port int
		if len(parts) == 2 {
			host = parts[0]
			fmt.Sscanf(parts[1], "%d", &port)
		}

		rsmembers = append(rsmembers, ReplicaSetMember{
			Host:      host,
			Port:      port,
			StateStr:  stateStr,
			StateNum:  state,
			Health:    health,
			IsPrimary: state == 1,
		})
	}

	return rsmembers, nil
}

// StepDownPrimary steps down the primary of a replica set
func StepDownPrimary(ctx context.Context, primaryHost string, rsName string, allHosts []string) (*FailoverEvent, error) {
	fmt.Printf("\n    Initiating primary stepdown for %s\n", primaryHost)
	fmt.Printf("    This will trigger a controlled failover...\n")

	startTime := time.Now()

	// Connect directly to the primary
	uri := fmt.Sprintf("mongodb://%s", primaryHost)
	clientOpts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetDirect(true)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to primary: %w", err)
	}
	defer client.Disconnect(ctx)

	// Record old primary
	oldPrimary := primaryHost

	// Execute replSetStepDown command
	// stepDownSecs: how long to step down (60 seconds)
	// secondaryCatchUpPeriodSecs: wait for secondaries to catch up (10 seconds)
	adminDB := client.Database("admin")
	var result bson.M
	err = adminDB.RunCommand(ctx, bson.D{
		{Key: "replSetStepDown", Value: 60},
		{Key: "secondaryCatchUpPeriodSecs", Value: 10},
	}).Decode(&result)

	// Note: replSetStepDown closes the connection, so we expect an error
	// But the command should succeed on the server side
	if err != nil && !strings.Contains(err.Error(), "connection") && !strings.Contains(err.Error(), "socket") {
		return nil, fmt.Errorf("stepdown command failed: %w", err)
	}

	fmt.Printf("    ✓ Stepdown command issued\n")
	fmt.Printf("    Waiting for new primary election...\n")

	// Wait for new primary election
	time.Sleep(3 * time.Second)

	// Get new replica set status to find new primary
	newPrimary := ""
	maxWait := 30 * time.Second
	elapsed := time.Duration(0)

	for elapsed < maxWait {
		members, err := GetReplicaSetStatus(ctx, rsName, allHosts)
		if err == nil {
			for _, member := range members {
				if member.IsPrimary {
					newPrimary = fmt.Sprintf("%s:%d", member.Host, member.Port)
					break
				}
			}
			if newPrimary != "" && newPrimary != oldPrimary {
				break
			}
		}
		time.Sleep(2 * time.Second)
		elapsed += 2 * time.Second
	}

	electionTime := time.Since(startTime)

	if newPrimary == "" {
		return nil, fmt.Errorf("no new primary elected within %v", maxWait)
	}

	fmt.Printf("    ✓ New primary elected: %s (election time: %v)\n", newPrimary, electionTime.Round(100*time.Millisecond))

	// Create failover event
	event := &FailoverEvent{
		Timestamp:      startTime,
		ReplicaSet:     rsName,
		OldPrimary:     oldPrimary,
		NewPrimary:     newPrimary,
		Reason:         "stepdown",
		ElectionTimeMS: int(electionTime.Milliseconds()),
	}

	return event, nil
}

// DetectNodeRole detects the role of a node in a replica set
func DetectNodeRole(ctx context.Context, hostPort string, rsName string, allHosts []string) (string, error) {
	members, err := GetReplicaSetStatus(ctx, rsName, allHosts)
	if err != nil {
		return "UNKNOWN", err
	}

	for _, member := range members {
		memberHostPort := fmt.Sprintf("%s:%d", member.Host, member.Port)
		if memberHostPort == hostPort {
			return member.StateStr, nil
		}
	}

	return "UNKNOWN", fmt.Errorf("node %s not found in replica set", hostPort)
}
