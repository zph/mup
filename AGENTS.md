# AGENTS.md

This file provides core design principles and architectural decisions for mup (MongoDB Utility Platform).

## Core Design Principle: Plan/Apply Pattern

**All operations that mutate cluster state MUST implement plan/apply semantics.**

This applies to: `deploy`, `upgrade`, `import`, and future `scale-out`/`scale-in` operations.

The pattern:
1. **Plan**: Generate an immutable execution plan by simulating the desired state
2. **Apply**: Execute plan steps, refreshing world state before each mutation and validating it matches the plan

This ensures idempotency, safety, and auditability. Inspired by Terraform/Kubernetes/ArgoCD but adapted for MongoDB's dynamic nature.

## Inheritance from TiUP

1. When deciding configuration behavior, especially deploy_dir, you SHOULD follow
https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/ for existing configurations.
Especially about location

## Architecture

### State Management
- **Playground**: `~/.mup/playground/` (JSON state, uses mongo-scaffold)
- **Production**: `~/.mup/storage/clusters/<name>/` (YAML metadata)
- **Binaries**: `~/.mup/storage/packages/` (versioned cache)

### Package Structure
- `cmd/mup/` - CLI commands (cobra)
- `pkg/apply/` - Plan/apply orchestration
- `pkg/deploy/` - Deployment phases (prepare/deploy/initialize/finalize)
- `pkg/plan/` - Plan generation and validation
- `pkg/cluster/` - Cluster lifecycle operations
- `pkg/executor/` - Execution abstraction (local/SSH)
- `pkg/template/` - Version-aware config templates
- `pkg/meta/` - Cluster metadata management

## Design Constraints

### Templating
- Multi-line string interpolation MUST use Go templates (not fmt.Sprintf)

### Binary Output
- All compiled binaries go to `./bin/` (never project root or `cmd/`)

### Documentation
- New markdown files MUST go in `docs/`

## Reference Documentation

### Architecture & Design
- `docs/DESIGN.md` - Comprehensive architectural design and technical decisions
- `docs/IMPLEMENTATION.md` - Implementation details and patterns
- `docs/specs/PLAN_APPLY_SYSTEM.md` - Plan/apply system specification

### Feature Documentation
- `docs/UPGRADE_ARCHITECTURE.md` - Upgrade system architecture
- `docs/UPGRADE_SPEC.md` - Upgrade specification
- `docs/HOOK_SPECIFICATION.md` - Hook system specification
- `docs/MONITORING_DESIGN.md` - Monitoring architecture
- `docs/MONITORING_GUIDE.md` - Monitoring implementation guide
- `docs/PLAYGROUND_DESIGN.md` - Playground feature design
- `docs/specs/cluster-import-requirements.md` - Cluster import requirements

### Implementation Plans
- `docs/TODO.md` - Implementation roadmap and phase breakdown
- `docs/DEPLOY_PLAN.md` - Deployment system plan
- `docs/SSHExecutor_PLAN.md` - SSH executor implementation plan
- `docs/SUPERVISORD_PLAN.md` - Supervisord integration plan
- `docs/SUPERVISORD_INTEGRATION_PROGRESS.md` - Supervisord integration progress tracking
- `docs/SUPERVISORD_SUMMARY.md` - Supervisord integration summary
- `docs/TEMPLATE_PLAN.md` - Template system plan
- `docs/TESTING_SSH.md` - SSH testing documentation

### Reference & Support
- `docs/MONGODB_UPGRADE_REFERENCE.md` - MongoDB version upgrade reference
- `docs/PERCONA_SUPPORT.md` - Percona Server support details
- `README.md` - User-facing documentation and quick start

### Code Structure
- `cmd/mup/` - CLI command definitions
- `pkg/apply/` - Plan/apply orchestration (`applier.go`, `state.go`, `checkpoint.go`, `hooks.go`)
- `pkg/plan/` - Plan generation (`plan.go`, `planner.go`, `validator.go`)
- `pkg/deploy/` - Deployment phases (`deployer.go`, `deploy.go`, `prepare.go`, `initialize.go`, `finalize.go`, `planner.go`)
- `pkg/upgrade/` - Upgrade operations
- `pkg/import/` - Cluster import operations
- `pkg/operation/` - Operation execution handlers

## Key Dependencies
- `github.com/spf13/cobra` - CLI framework
- `github.com/zph/mongo-scaffold` - Playground cluster orchestration
