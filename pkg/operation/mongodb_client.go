package operation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/zph/mup/pkg/executor"
)

// MongoDBClient abstracts MongoDB operations for both simulation and real execution
// This ensures simulation perfectly mirrors real execution
type MongoDBClient struct {
	executor     executor.Executor
	isSimulation bool
	realClient   *mongo.Client
	connStr      string
	host         string
}

// NewMongoDBClient creates a client that works in both simulation and real modes
// For direct connections to mongod nodes (replica set members)
func NewMongoDBClient(ctx context.Context, host string, exec executor.Executor) (*MongoDBClient, error) {
	return newMongoDBClient(ctx, host, exec, true)
}

// NewMongoDBClientForMongos creates a client for connecting to mongos (not direct)
func NewMongoDBClientForMongos(ctx context.Context, host string, exec executor.Executor) (*MongoDBClient, error) {
	return newMongoDBClient(ctx, host, exec, false)
}

func newMongoDBClient(ctx context.Context, host string, exec executor.Executor, direct bool) (*MongoDBClient, error) {
	execType := fmt.Sprintf("%T", exec)
	isSimulation := strings.Contains(execType, "Simulation")

	client := &MongoDBClient{
		executor:     exec,
		isSimulation: isSimulation,
		host:         host,
		connStr:      fmt.Sprintf("mongodb://%s", host),
	}

	if isSimulation {
		// Record connection in simulation using dedicated MongoDB method
		exec.MongoExecute(host, fmt.Sprintf("mongo.Connect(%s)", client.connStr))
	} else {
		// Real connection
		opts := options.Client().
			ApplyURI(client.connStr).
			SetServerSelectionTimeout(10*time.Second).
			SetConnectTimeout(10*time.Second)

		if direct {
			opts.SetDirect(true)
		}

		realClient, err := mongo.Connect(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to %s: %w", host, err)
		}
		client.realClient = realClient
	}

	return client, nil
}

// RunCommand executes a MongoDB admin command
// In simulation mode: records the command
// In real mode: actually executes it
func (c *MongoDBClient) RunCommand(ctx context.Context, cmd bson.M, isSafetyCheck bool) (bson.M, error) {
	cmdJSON, _ := bson.MarshalExtJSON(cmd, false, false)

	if c.isSimulation {
		// Record command in simulation using dedicated MongoDB method
		suffix := ""
		if isSafetyCheck {
			suffix = " [safety check]"
		}
		c.executor.MongoExecute(c.host, fmt.Sprintf("client.Database(\"admin\").RunCommand(%s)%s", string(cmdJSON), suffix))

		// For safety checks, simulate "not found" state so the flow continues
		// This ensures simulation shows all steps that would happen on first run
		if isSafetyCheck {
			// Simulate the command returning an error (e.g., "not initialized")
			return nil, fmt.Errorf("simulated: resource not yet initialized")
		}

		// For non-safety-check commands, return realistic simulated responses
		// Check command type to return appropriate structure
		if _, hasReplSetGetStatus := cmd["replSetGetStatus"]; hasReplSetGetStatus {
			// Return simulated replSetGetStatus with PRIMARY elected
			return bson.M{
				"ok": 1,
				"members": bson.A{
					bson.M{
						"_id":      0,
						"name":     c.host,
						"stateStr": "PRIMARY",
						"health":   1,
					},
				},
			}, nil
		}

		// Default: return simple success
		return bson.M{"ok": 1}, nil
	}

	// Real execution
	var result bson.M
	err := c.realClient.Database("admin").RunCommand(ctx, cmd).Decode(&result)
	return result, err
}

// Disconnect closes the connection
func (c *MongoDBClient) Disconnect(ctx context.Context) error {
	if c.isSimulation {
		return nil
	}
	if c.realClient != nil {
		return c.realClient.Disconnect(ctx)
	}
	return nil
}
