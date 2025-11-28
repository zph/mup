package operation

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
)

// TestInitReplicaSetHandler_Simulation tests InitReplicaSetHandler in simulation mode
// REQ-SIM-001: MongoDB operations work transparently in simulation mode
func TestInitReplicaSetHandler_Simulation(t *testing.T) {
	handler := &InitReplicaSetHandler{}
	ctx := context.Background()

	// Create simulation executor
	simConfig := simulation.NewConfig()
	simExec := simulation.NewExecutor(simConfig)

	// Create planned operation
	op := &plan.PlannedOperation{
		Type: plan.OpInitReplicaSet,
		Params: map[string]interface{}{
			"replica_set": "rs0",
			"members":     []string{"localhost:27017", "localhost:27018", "localhost:27019"},
		},
		Changes: []plan.Change{
			{
				ResourceType: "replica_set",
				ResourceID:   "rs0",
				Action:       "create",
			},
		},
	}

	// Execute operation
	result, err := handler.Execute(ctx, op, simExec)

	// Verify no error
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify successful result
	assert.True(t, result.Success)
	assert.Contains(t, result.Output, "rs0")

	// Verify simulation executor recorded all MongoDB commands
	ops := simExec.GetOperations()
	assert.Greater(t, len(ops), 0, "Expected at least one operation to be recorded")

	// Should have: Connect, replSetGetStatus (safety check), replSetInitiate, replSetGetStatus (verification)
	assert.GreaterOrEqual(t, len(ops), 4, "Expected at least 4 operations (connect, safety check, initiate, verification)")

	// Verify we see the connection and all operation types
	foundConnect := false
	foundSafetyCheck := false
	foundInitiate := false
	foundVerification := false

	for _, op := range ops {
		if op.Type == "mongo_execute" {
			if strings.Contains(op.Details, "mongo.Connect") {
				foundConnect = true
				assert.Contains(t, op.Target, "localhost:27017")
			}
			if strings.Contains(op.Details, "replSetGetStatus") {
				if strings.Contains(op.Details, "[safety check]") {
					foundSafetyCheck = true
				} else {
					// Post-initialization verification (no [safety check] marker)
					foundVerification = true
				}
			}
			if strings.Contains(op.Details, "replSetInitiate") {
				foundInitiate = true
				assert.Contains(t, op.Details, "client.Database")
				assert.Contains(t, op.Details, "rs0")
			}
		}
	}

	assert.True(t, foundConnect, "Expected to find mongo.Connect operation")
	assert.True(t, foundSafetyCheck, "Expected to find replSetGetStatus safety check")
	assert.True(t, foundInitiate, "Expected to find replSetInitiate command")
	assert.True(t, foundVerification, "Expected to find replSetGetStatus verification")
}

// TestInitReplicaSetHandler_Validation tests parameter validation
func TestInitReplicaSetHandler_Validation(t *testing.T) {
	handler := &InitReplicaSetHandler{}
	ctx := context.Background()
	simExec := simulation.NewExecutor(simulation.NewConfig())

	tests := []struct {
		name    string
		op      *plan.PlannedOperation
		wantErr bool
	}{
		{
			name: "valid parameters",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"replica_set": "rs0",
					"members":     []string{"localhost:27017"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing replica_set",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"members": []string{"localhost:27017"},
				},
			},
			wantErr: true,
		},
		{
			name: "missing members",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"replica_set": "rs0",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handler.PreHook(ctx, tt.op, simExec)
			if tt.wantErr {
				if err != nil {
					assert.Error(t, err)
				} else {
					assert.NotNil(t, result)
					assert.False(t, result.Valid, "expected validation to fail")
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.True(t, result.Valid, "expected validation to pass")
			}
		})
	}
}

// TestAddShardHandler_Simulation tests AddShardHandler in simulation mode
// REQ-SIM-001: MongoDB operations work transparently in simulation mode
func TestAddShardHandler_Simulation(t *testing.T) {
	handler := &AddShardHandler{}
	ctx := context.Background()

	// Create simulation executor
	simConfig := simulation.NewConfig()
	simExec := simulation.NewExecutor(simConfig)

	// Create planned operation
	op := &plan.PlannedOperation{
		Type: plan.OpAddShard,
		Params: map[string]interface{}{
			"shard_name":        "shard1",
			"connection_string": "shard1/localhost:27017",
			"mongos_host":       "localhost:27016",
		},
		Changes: []plan.Change{
			{
				ResourceType: "shard",
				ResourceID:   "shard1",
				Action:       "create",
			},
		},
	}

	// Execute operation
	result, err := handler.Execute(ctx, op, simExec)

	// Verify no error
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify successful result
	assert.True(t, result.Success)
	assert.Contains(t, result.Output, "shard1")

	// Verify simulation executor recorded all MongoDB commands
	ops := simExec.GetOperations()
	assert.Greater(t, len(ops), 0, "Expected at least one operation to be recorded")

	// Should have: Connect, listShards (safety check), addShard
	assert.GreaterOrEqual(t, len(ops), 3, "Expected at least 3 operations (connect, safety check, addShard)")

	// Verify we see all the operations
	foundConnect := false
	foundSafetyCheck := false
	foundAddShard := false

	for _, op := range ops {
		if op.Type == "mongo_execute" {
			if strings.Contains(op.Details, "mongo.Connect") {
				foundConnect = true
				assert.Contains(t, op.Target, "localhost:27016")
			}
			if strings.Contains(op.Details, "listShards") {
				foundSafetyCheck = true
				assert.Contains(t, op.Details, "[safety check]")
			}
			if strings.Contains(op.Details, "addShard") && !strings.Contains(op.Details, "listShards") {
				foundAddShard = true
				assert.Contains(t, op.Details, "client.Database")
				assert.Contains(t, op.Details, "shard1")
			}
		}
	}

	assert.True(t, foundConnect, "Expected to find mongo.Connect operation")
	assert.True(t, foundSafetyCheck, "Expected to find listShards safety check")
	assert.True(t, foundAddShard, "Expected to find addShard command")
}

// TestAddShardHandler_Validation tests parameter validation
func TestAddShardHandler_Validation(t *testing.T) {
	handler := &AddShardHandler{}
	ctx := context.Background()
	simExec := simulation.NewExecutor(simulation.NewConfig())

	tests := []struct {
		name    string
		op      *plan.PlannedOperation
		wantErr bool
	}{
		{
			name: "valid parameters",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"shard_name":        "shard1",
					"connection_string": "shard1/localhost:27017",
					"mongos_host":       "localhost:27016",
				},
			},
			wantErr: false,
		},
		{
			name: "missing shard_name",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"connection_string": "shard1/localhost:27017",
					"mongos_host":       "localhost:27016",
				},
			},
			wantErr: true,
		},
		{
			name: "missing connection_string",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"shard_name":  "shard1",
					"mongos_host": "localhost:27016",
				},
			},
			wantErr: true,
		},
		{
			name: "missing mongos_host",
			op: &plan.PlannedOperation{
				Params: map[string]interface{}{
					"shard_name":        "shard1",
					"connection_string": "shard1/localhost:27017",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handler.PreHook(ctx, tt.op, simExec)
			if tt.wantErr {
				if err != nil {
					assert.Error(t, err)
				} else {
					assert.NotNil(t, result)
					assert.False(t, result.Valid, "expected validation to fail")
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.True(t, result.Valid, "expected validation to pass")
			}
		})
	}
}

// TestInitReplicaSetHandler_EmptyMembers tests error handling for empty members
func TestInitReplicaSetHandler_EmptyMembers(t *testing.T) {
	handler := &InitReplicaSetHandler{}
	ctx := context.Background()
	simExec := simulation.NewExecutor(simulation.NewConfig())

	op := &plan.PlannedOperation{
		Type: plan.OpInitReplicaSet,
		Params: map[string]interface{}{
			"replica_set": "rs0",
			"members":     []string{}, // Empty members list
		},
	}

	result, err := handler.Execute(ctx, op, simExec)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no members")
}
