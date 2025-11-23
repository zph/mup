# Lifecycle Hook Specification

**Version**: 1.0
**Last Updated**: 2025-01-22

## Overview

Mup's upgrade workflow provides lifecycle hooks at critical points during the upgrade process. Hooks allow integration with external systems such as monitoring, alerting, health checks, and custom validation logic.

Hooks can be:
- **Command Hooks**: Shell commands executed with environment variables
- **Function Hooks**: Go functions called directly (programmatic use only)

This specification defines the contract for command hooks, including all environment variables and data formats.

## Hook Execution Model

### Execution Flow

1. **Trigger**: Upgrade reaches a hook point (e.g., `before-node-upgrade`)
2. **Context Assembly**: Mup assembles hook context with all relevant information
3. **Environment Setup**: Context is exported as environment variables
4. **Command Execution**: Hook command runs with timeout
5. **Result Handling**: Exit code 0 = success, non-zero = failure (aborts upgrade)

### Timeout and Error Handling

- Default timeout: **30 seconds** (configurable per hook)
- Exit code 0: Hook succeeded, upgrade continues
- Exit code non-zero: Hook failed, upgrade aborts
- Timeout: Treated as failure, upgrade aborts
- Output: STDOUT/STDERR captured and displayed

## Environment Variables

All hooks receive the following environment variables:

### Standard Variables

Always present for all hook types:

| Variable | Type | Description | Example |
|----------|------|-------------|---------|
| `MUP_HOOK_TYPE` | string | Type of hook being executed | `before-node-upgrade` |
| `MUP_CLUSTER_NAME` | string | Name of the cluster being upgraded | `prod-cluster-01` |
| `MUP_FROM_VERSION` | string | Current MongoDB version | `mongo-6.0.15` |
| `MUP_TO_VERSION` | string | Target MongoDB version | `mongo-7.0.0` |
| `MUP_PHASE` | string | Current upgrade phase | `config-servers` |

### Node-Level Variables

Present for node-specific hooks (`before-node-upgrade`, `after-node-upgrade`, etc.):

| Variable | Type | Description | Example |
|----------|------|-------------|---------|
| `MUP_NODE` | string | Node host:port | `localhost:27017` |
| `MUP_NODE_ROLE` | string | Node role in cluster | `PRIMARY`, `SECONDARY`, `MONGOS`, `CONFIG` |
| `MUP_ATTEMPT` | int | Retry attempt number (1-indexed) | `1` |

### Shard-Level Variables

Present for shard-specific hooks (`before-shard-upgrade`, `after-shard-upgrade`):

| Variable | Type | Description | Example |
|----------|------|-------------|---------|
| `MUP_SHARD_NAME` | string | Name of the shard | `shard-01` |

### Error Variables

Present for failure hooks (`on-node-failure`, `on-upgrade-failure`):

| Variable | Type | Description | Example |
|----------|------|-------------|---------|
| `MUP_ERROR` | string | Error message that caused failure | `failed to stop process: connection refused` |

### Custom Metadata Variables

Hooks can receive custom metadata via `MUP_META_*` environment variables:

| Variable | Type | Description | Example |
|----------|------|-------------|---------|
| `MUP_META_<KEY>` | string | Custom key-value metadata | `MUP_META_DATACENTER=us-west-2` |

Keys are uppercased. Example: metadata `{"datacenter": "us-west-2"}` becomes `MUP_META_DATACENTER=us-west-2`.

## JSON Format (Optional)

For complex integrations, hook context can also be passed as JSON via stdin or a temporary file. This is optional and must be explicitly configured.

### HookContext JSON Schema

```json
{
  "hook_type": "before-node-upgrade",
  "cluster_name": "prod-cluster-01",
  "phase": "config-servers",
  "node": "localhost:27017",
  "node_role": "PRIMARY",
  "shard_name": "shard-01",
  "from_version": "mongo-6.0.15",
  "to_version": "mongo-7.0.0",
  "attempt_count": 1,
  "error": null,
  "metadata": {
    "datacenter": "us-west-2",
    "environment": "production"
  }
}
```

### Field Descriptions

- `hook_type` (string): Hook type identifier
- `cluster_name` (string): Cluster name
- `phase` (string): Current phase: `pre-flight`, `config-servers`, `shard`, `mongos`, `post-upgrade`
- `node` (string, optional): Node host:port (empty for non-node hooks)
- `node_role` (string, optional): Node role (empty for non-node hooks)
- `shard_name` (string, optional): Shard name (empty for non-shard hooks)
- `from_version` (string): Current version
- `to_version` (string): Target version
- `attempt_count` (int): Retry attempt (1-indexed)
- `error` (string, nullable): Error message (null unless failure hook)
- `metadata` (object): Custom key-value metadata

## Hook Types Reference

### Node-Level Hooks

#### `before-node-upgrade`

**When**: Before upgrading a single node (before stopping the process)

**Use Cases**:
- External health check before touching node
- Notify monitoring system of planned downtime
- Custom pre-flight validation for specific node

**Available Context**: All standard + node-level variables

**Example**:
```bash
#!/bin/bash
# Notify monitoring system
curl -X POST https://monitoring.example.com/api/maintenance \
  -H "Content-Type: application/json" \
  -d "{
    \"cluster\": \"$MUP_CLUSTER_NAME\",
    \"node\": \"$MUP_NODE\",
    \"status\": \"starting_upgrade\"
  }"
```

#### `after-node-upgrade`

**When**: After upgrading a single node (after process has restarted)

**Use Cases**:
- Verify node health externally
- Wait for custom metrics to stabilize
- Notify monitoring system upgrade completed

**Available Context**: All standard + node-level variables

**Example**:
```bash
#!/bin/bash
# Wait for node to appear healthy in monitoring
for i in {1..30}; do
  STATUS=$(curl -sf "https://monitoring.example.com/api/health/$MUP_NODE")
  if echo "$STATUS" | grep -q "healthy"; then
    echo "Node is healthy"
    exit 0
  fi
  echo "Attempt $i: Node not healthy yet, waiting..."
  sleep 5
done
echo "Node did not become healthy within 150 seconds"
exit 1
```

#### `on-node-failure`

**When**: When a node upgrade fails

**Use Cases**:
- Send alert to on-call team
- Create incident ticket
- Log failure to external system

**Available Context**: All standard + node-level + error variables

**Example**:
```bash
#!/bin/bash
# Send PagerDuty alert
curl -X POST https://api.pagerduty.com/incidents \
  -H "Authorization: Token token=$PAGERDUTY_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"incident\": {
      \"type\": \"incident\",
      \"title\": \"MongoDB Upgrade Failed: $MUP_CLUSTER_NAME/$MUP_NODE\",
      \"body\": {
        \"type\": \"incident_body\",
        \"details\": \"Error: $MUP_ERROR\"
      },
      \"urgency\": \"high\"
    }
  }"
```

### Replica Set Hooks

#### `before-primary-stepdown`

**When**: Before stepping down the primary (critical operation)

**Use Cases**:
- Notify application team of planned primary change
- Wait for application to drain connections
- Custom validation before failover

**Available Context**: All standard + node-level variables (node = primary)

**Example**:
```bash
#!/bin/bash
# Notify application via webhook
curl -X POST https://app.example.com/api/prepare-for-failover \
  -H "Content-Type: application/json" \
  -d "{
    \"cluster\": \"$MUP_CLUSTER_NAME\",
    \"current_primary\": \"$MUP_NODE\",
    \"action\": \"drain_connections\"
  }"

# Wait for confirmation
echo "Waiting 30 seconds for application to drain connections..."
sleep 30
```

#### `after-primary-stepdown`

**When**: After primary has stepped down

**Use Cases**:
- Verify new primary elected
- Confirm application reconnected
- Log primary change event

**Available Context**: All standard + node-level variables

**Example**:
```bash
#!/bin/bash
echo "Primary stepdown completed for $MUP_NODE"
echo "Cluster: $MUP_CLUSTER_NAME"
echo "Phase: $MUP_PHASE"
```

#### `before-secondary-upgrade`

**When**: Before upgrading a secondary node

**Available Context**: All standard + node-level variables

#### `after-secondary-upgrade`

**When**: After upgrading a secondary node

**Available Context**: All standard + node-level variables

### Shard Hooks

#### `before-shard-upgrade`

**When**: Before starting upgrade of an entire shard

**Use Cases**:
- Notify team which shard is being upgraded
- Check shard-specific prerequisites
- Log shard upgrade start

**Available Context**: All standard + shard-level variables

**Example**:
```bash
#!/bin/bash
echo "Starting upgrade of shard: $MUP_SHARD_NAME"
echo "Cluster: $MUP_CLUSTER_NAME"
echo "Upgrading from $MUP_FROM_VERSION to $MUP_TO_VERSION"

# Optional: Check external dependency
curl -sf https://monitoring.example.com/api/shard/$MUP_SHARD_NAME/ready || {
  echo "Shard not ready according to external system"
  exit 1
}
```

#### `after-shard-upgrade`

**When**: After completing upgrade of an entire shard

**Use Cases**:
- Verify shard health
- Run shard-level validation
- Notify completion

**Available Context**: All standard + shard-level variables

### Phase Hooks

#### `before-phase`

**When**: Before starting a new phase (e.g., before starting config server upgrades)

**Use Cases**:
- Log phase transitions
- Wait for operator approval (interactive)
- Check phase-specific prerequisites

**Available Context**: All standard variables (no node/shard specifics)

**Example**:
```bash
#!/bin/bash
echo "Starting phase: $MUP_PHASE"
echo "Cluster: $MUP_CLUSTER_NAME"

# Could prompt for manual approval if needed
# read -p "Press Enter to continue with $MUP_PHASE..."
```

#### `after-phase`

**When**: After completing a phase

**Use Cases**:
- Log phase completion
- Run phase-level validation
- Notify monitoring system

**Available Context**: All standard variables

### Critical Operation Hooks

#### `before-fcv-upgrade`

**When**: Before upgrading Feature Compatibility Version (FCV)

**Use Cases**:
- Final validation before FCV upgrade (irreversible)
- Backup FCV state
- Notify stakeholders

**Available Context**: All standard variables

**Example**:
```bash
#!/bin/bash
echo "CRITICAL: About to upgrade FCV for $MUP_CLUSTER_NAME"
echo "From: $MUP_FROM_VERSION to $MUP_TO_VERSION"
echo "This operation cannot be rolled back"

# Could check with external approval system
curl -sf https://approval.example.com/api/check-fcv-upgrade/$MUP_CLUSTER_NAME || {
  echo "FCV upgrade not approved"
  exit 1
}
```

#### `after-fcv-upgrade`

**When**: After FCV upgrade completes

**Available Context**: All standard variables

#### `before-balancer-stop`

**When**: Before stopping the balancer (sharded clusters only)

**Available Context**: All standard variables

#### `after-balancer-start`

**When**: After restarting the balancer (sharded clusters only)

**Available Context**: All standard variables

### Global Hooks

#### `on-upgrade-start`

**When**: At the very beginning of the upgrade process

**Use Cases**:
- Create incident/change ticket
- Notify all stakeholders
- Initialize external tracking

**Available Context**: All standard variables

**Example**:
```bash
#!/bin/bash
# Create ServiceNow change ticket
TICKET=$(curl -X POST https://servicenow.example.com/api/change \
  -u "$SERVICENOW_USER:$SERVICENOW_PASS" \
  -H "Content-Type: application/json" \
  -d "{
    \"short_description\": \"MongoDB Upgrade: $MUP_CLUSTER_NAME\",
    \"description\": \"Upgrading from $MUP_FROM_VERSION to $MUP_TO_VERSION\",
    \"type\": \"standard\",
    \"risk\": \"medium\"
  }" | jq -r '.result.number')

echo "Created change ticket: $TICKET"
echo "$TICKET" > /tmp/mup-upgrade-ticket.txt
```

#### `on-upgrade-complete`

**When**: After successful completion of all upgrade phases

**Use Cases**:
- Close change ticket
- Send success notification
- Update inventory systems

**Available Context**: All standard variables

**Example**:
```bash
#!/bin/bash
TICKET=$(cat /tmp/mup-upgrade-ticket.txt)

# Close ServiceNow ticket
curl -X PATCH "https://servicenow.example.com/api/change/$TICKET" \
  -u "$SERVICENOW_USER:$SERVICENOW_PASS" \
  -H "Content-Type: application/json" \
  -d '{"state": "closed", "close_notes": "Upgrade completed successfully"}'

# Send Slack notification
curl -X POST "$SLACK_WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -d "{
    \"text\": \"✅ MongoDB Upgrade Complete: $MUP_CLUSTER_NAME\",
    \"blocks\": [{
      \"type\": \"section\",
      \"text\": {
        \"type\": \"mrkdwn\",
        \"text\": \"*Upgrade Complete*\n• Cluster: $MUP_CLUSTER_NAME\n• Version: $MUP_FROM_VERSION → $MUP_TO_VERSION\n• Change: $TICKET\"
      }
    }]
  }"
```

#### `on-upgrade-failure`

**When**: When upgrade fails and cannot continue

**Use Cases**:
- Send critical alert
- Page on-call team
- Update change ticket with failure

**Available Context**: All standard + error variables

**Example**:
```bash
#!/bin/bash
# Send critical Slack alert
curl -X POST "$SLACK_WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -d "{
    \"text\": \"🚨 MongoDB Upgrade FAILED: $MUP_CLUSTER_NAME\",
    \"blocks\": [{
      \"type\": \"section\",
      \"text\": {
        \"type\": \"mrkdwn\",
        \"text\": \"*UPGRADE FAILED*\n• Cluster: $MUP_CLUSTER_NAME\n• Phase: $MUP_PHASE\n• Error: \`$MUP_ERROR\`\"
      }
    }]
  }"

# Page on-call
curl -X POST https://api.pagerduty.com/incidents \
  -H "Authorization: Token token=$PAGERDUTY_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"incident\": {
      \"type\": \"incident\",
      \"title\": \"MongoDB Upgrade Failed: $MUP_CLUSTER_NAME\",
      \"urgency\": \"high\"
    }
  }"
```

## Wait Time Configuration

In addition to hooks, Mup provides configurable wait times at various points:

### WaitConfig Structure

```yaml
wait_config:
  # Node-level waits
  after_node_upgrade: 5s       # Wait after each node restarts
  after_node_failure: 10s      # Wait after node failure before retry

  # Replica set waits
  after_primary_stepdown: 30s  # Wait for new primary election
  after_secondary_upgrade: 10s # Wait for secondary to catch up
  before_primary_upgrade: 15s  # Wait before upgrading primary

  # Shard waits
  between_shards: 60s          # Wait between shard upgrades
  after_shard_complete: 30s    # Wait after shard completes

  # Phase waits
  between_phases: 10s          # Wait between phases

  # Critical operation waits
  after_balancer_stop: 30s     # Wait for balancer to stop
  after_fcv_upgrade: 60s       # Wait for FCV to propagate

  # Health check intervals
  health_check_interval: 5s    # How often to check health
  health_check_timeout: 300s   # Max time to wait for health
```

### Usage in Upgrade Config

```go
waitConfig := &upgrade.WaitConfig{
    AfterNodeUpgrade:      10 * time.Second,  // Override default
    BetweenShards:         120 * time.Second, // Wait 2 minutes between shards
    AfterPrimaryStepdown:  45 * time.Second,
}

config := upgrade.UpgradeConfig{
    // ... other config
    WaitConfig: waitConfig,
}
```

## Integration Patterns

### Pattern 1: External Health Check

Wait for external monitoring system to report node healthy:

```bash
#!/bin/bash
# Hook: after-node-upgrade
# Timeout: 5 minutes

HEALTH_URL="https://monitoring.example.com/api/health"
MAX_ATTEMPTS=60
INTERVAL=5

for i in $(seq 1 $MAX_ATTEMPTS); do
  RESPONSE=$(curl -sf "$HEALTH_URL/$MUP_NODE")

  if echo "$RESPONSE" | jq -e '.status == "healthy"' > /dev/null; then
    echo "✓ Node $MUP_NODE is healthy"
    exit 0
  fi

  echo "Attempt $i/$MAX_ATTEMPTS: Node not healthy yet"
  sleep $INTERVAL
done

echo "✗ Node did not become healthy within $(($MAX_ATTEMPTS * $INTERVAL)) seconds"
exit 1
```

### Pattern 2: Slack Notifications

Send rich Slack notifications at key points:

```bash
#!/bin/bash
# Hook: before-node-upgrade, after-node-upgrade, on-node-failure

send_slack() {
  local emoji="$1"
  local status="$2"
  local color="$3"

  curl -X POST "$SLACK_WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -d "{
      \"text\": \"$emoji MongoDB Upgrade: $MUP_CLUSTER_NAME\",
      \"attachments\": [{
        \"color\": \"$color\",
        \"fields\": [
          {\"title\": \"Status\", \"value\": \"$status\", \"short\": true},
          {\"title\": \"Node\", \"value\": \"$MUP_NODE ($MUP_NODE_ROLE)\", \"short\": true},
          {\"title\": \"Phase\", \"value\": \"$MUP_PHASE\", \"short\": true},
          {\"title\": \"Version\", \"value\": \"$MUP_FROM_VERSION → $MUP_TO_VERSION\", \"short\": true}
        ]
      }]
    }"
}

case "$MUP_HOOK_TYPE" in
  before-node-upgrade)
    send_slack "🔄" "Starting upgrade" "warning"
    ;;
  after-node-upgrade)
    send_slack "✅" "Upgrade complete" "good"
    ;;
  on-node-failure)
    send_slack "🚨" "Upgrade FAILED: $MUP_ERROR" "danger"
    ;;
esac
```

### Pattern 3: Custom Validation

Run custom validation before critical operations:

```bash
#!/bin/bash
# Hook: before-primary-stepdown
# Ensure replication lag is acceptable

MONGOSH="/path/to/mongosh"
MAX_LAG_SECONDS=10

# Connect to cluster and check replication lag
LAG=$($MONGOSH --quiet --eval "
  rs.status().members
    .filter(m => m.state === 2)
    .map(m => Math.abs(m.optimeDate - new Date()) / 1000)
    .reduce((max, lag) => Math.max(max, lag), 0)
")

if (( $(echo "$LAG > $MAX_LAG_SECONDS" | bc -l) )); then
  echo "✗ Replication lag too high: ${LAG}s (max: ${MAX_LAG_SECONDS}s)"
  echo "Not safe to step down primary"
  exit 1
fi

echo "✓ Replication lag acceptable: ${LAG}s"
```

### Pattern 4: Wait for Manual Approval

Pause upgrade for operator approval:

```bash
#!/bin/bash
# Hook: before-fcv-upgrade

echo "========================================="
echo "CRITICAL OPERATION: FCV Upgrade"
echo "========================================="
echo "Cluster: $MUP_CLUSTER_NAME"
echo "Current Phase: $MUP_PHASE"
echo "FCV Change: $MUP_FROM_VERSION → $MUP_TO_VERSION"
echo ""
echo "⚠️  FCV upgrade is IRREVERSIBLE"
echo "⚠️  Ensure all nodes are upgraded first"
echo ""
read -p "Type 'CONFIRM' to proceed: " confirmation

if [ "$confirmation" != "CONFIRM" ]; then
  echo "❌ FCV upgrade cancelled"
  exit 1
fi

echo "✅ FCV upgrade approved"
```

### Pattern 5: Incident Management Integration

Create and update incident tickets:

```bash
#!/bin/bash
# Hook: on-upgrade-start, on-upgrade-complete, on-upgrade-failure

INCIDENT_FILE="/tmp/mup-incident-${MUP_CLUSTER_NAME}.json"

create_incident() {
  curl -X POST https://incident.example.com/api/incidents \
    -H "Content-Type: application/json" \
    -d "{
      \"title\": \"MongoDB Upgrade: $MUP_CLUSTER_NAME\",
      \"description\": \"Upgrading from $MUP_FROM_VERSION to $MUP_TO_VERSION\",
      \"severity\": \"low\",
      \"status\": \"in_progress\"
    }" | tee "$INCIDENT_FILE"
}

update_incident() {
  local status="$1"
  local incident_id=$(jq -r '.id' "$INCIDENT_FILE")

  curl -X PATCH "https://incident.example.com/api/incidents/$incident_id" \
    -H "Content-Type: application/json" \
    -d "{\"status\": \"$status\"}"
}

case "$MUP_HOOK_TYPE" in
  on-upgrade-start)
    create_incident
    ;;
  on-upgrade-complete)
    update_incident "resolved"
    ;;
  on-upgrade-failure)
    update_incident "critical"
    ;;
esac
```

## Hook Registration (Programmatic)

For Go code using Mup as a library:

```go
import "github.com/zph/mup/pkg/upgrade"

// Create hook registry
registry := upgrade.NewHookRegistry()

// Register command hook
healthHook := upgrade.CreateHealthCheckHook("https://monitoring.example.com/health")
registry.Register(healthHook)

// Register custom command hook
notifyHook := upgrade.NewCommandHook(
    "slack-notification",
    upgrade.HookAfterNodeUpgrade,
    `curl -X POST $SLACK_WEBHOOK -d '{"text": "Node upgraded: $MUP_NODE"}'`,
    30*time.Second,
)
registry.Register(notifyHook)

// Register function hook
customHook := upgrade.NewFunctionHook(
    "custom-validation",
    upgrade.HookBeforePrimaryStepdown,
    func(ctx context.Context, hookCtx upgrade.HookContext) error {
        // Custom Go logic
        fmt.Printf("Validating %s before stepdown\n", hookCtx.Node)
        return nil
    },
)
registry.Register(customHook)

// Pass registry to upgrade config
config := upgrade.UpgradeConfig{
    // ... other config
    HookRegistry: registry,
}
```

## Hook Configuration File (YAML)

Future enhancement: Define hooks in YAML configuration:

```yaml
hooks:
  before-node-upgrade:
    - name: health-check
      command: /usr/local/bin/check-node-health.sh
      timeout: 5m
      env:
        HEALTH_ENDPOINT: https://monitoring.example.com

  after-node-upgrade:
    - name: notify-slack
      command: |
        curl -X POST $SLACK_WEBHOOK_URL \
          -d '{"text": "Node upgraded: $MUP_NODE ($MUP_NODE_ROLE)"}'
      timeout: 10s

  on-upgrade-failure:
    - name: page-oncall
      command: /usr/local/bin/page-oncall.sh
      timeout: 30s
      env:
        PAGERDUTY_SERVICE: mongodb-prod
```

## Security Considerations

1. **Hook Commands**: Run with mup process permissions - use principle of least privilege
2. **Secrets**: Never pass secrets via environment variables - use credential stores
3. **Timeouts**: Always set reasonable timeouts to prevent hanging upgrades
4. **Validation**: Validate hook exit codes - non-zero should abort upgrade
5. **Logging**: All hook output is logged - avoid logging secrets
6. **Audit**: Hook execution should be audited for compliance

## Best Practices

1. **Idempotency**: Design hooks to be idempotent (safe to run multiple times)
2. **Timeouts**: Set realistic timeouts - default 30s may be too short for external systems
3. **Error Messages**: Provide clear error messages for troubleshooting
4. **Retries**: Implement retries in hook scripts for transient failures
5. **Testing**: Test hooks independently before upgrade
6. **Monitoring**: Use `after-node-upgrade` for health validation, not assumptions
7. **Notifications**: Send notifications for critical operations only (avoid alert fatigue)
8. **Documentation**: Document all custom hooks in your runbook

## Troubleshooting

### Hook Timeout

**Symptom**: Hook times out after 30 seconds (or configured timeout)

**Solutions**:
- Increase timeout when registering hook
- Check if external system is slow/unreachable
- Add retries with exponential backoff in hook script

### Hook Fails Silently

**Symptom**: Hook exits 0 but didn't work correctly

**Solutions**:
- Check STDOUT/STDERR in upgrade logs
- Add verbose logging to hook script (`set -x`)
- Verify environment variables are set correctly

### Environment Variables Not Available

**Symptom**: Hook script can't access `$MUP_*` variables

**Solutions**:
- Verify hook is registered with correct hook type
- Check that context includes needed information
- Print all env vars for debugging: `env | grep MUP_`

### Hook Blocks Upgrade Forever

**Symptom**: Upgrade hangs during hook execution

**Solutions**:
- Always set explicit timeouts
- Avoid interactive prompts in automated mode
- Check if hook command is waiting for stdin

## Future Enhancements

- **Hook Templates**: Pre-built hooks for common integrations (Slack, PagerDuty, etc.)
- **YAML Configuration**: Define hooks in cluster config file
- **Async Hooks**: Non-blocking hooks for notifications
- **Hook Chaining**: Multiple hooks per hook point with dependency management
- **Hook Library**: Shareable hook scripts repository

## References

- [UPG-009] Lifecycle hooks and wait configuration (UPGRADE_SPEC.md)
- [UPG-016] Configurable user prompting (UPGRADE_SPEC.md)
- `pkg/upgrade/hooks.go` - Hook implementation
- `pkg/upgrade/upgrade.go` - Upgrade workflow with hook integration
