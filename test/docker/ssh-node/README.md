# SSH Node Test Container

This Docker image provides an SSH-enabled Ubuntu 22.04 container for testing SSH-based MongoDB deployments.

## Features

- Ubuntu 22.04 base
- OpenSSH server pre-configured
- Test user `testuser` with sudo access
- Support for both key-based and password authentication
- Pre-created directories for MongoDB data, logs, and config
- Common utilities pre-installed (curl, wget, lsof, net-tools, etc.)

## Building

```bash
cd test/docker/ssh-node
docker build -t mup-ssh-node:latest .
```

## Running Manually

```bash
# Run with password authentication
docker run -d -p 2222:22 --name mup-test-node mup-ssh-node:latest

# Connect with password (testpass)
ssh -p 2222 testuser@localhost

# Run with key-based authentication
docker run -d \
  -p 2222:22 \
  -v ~/.ssh/id_ed25519_mup_test.pub:/home/testuser/.ssh/authorized_keys:ro \
  --name mup-test-node \
  mup-ssh-node:latest

# Connect with key
ssh -i ~/.ssh/id_ed25519_mup_test -p 2222 testuser@localhost
```

## Usage with Testcontainers

This image is designed to be used with the `testcontainers-go` library. See `pkg/executor/testcontainer_helpers.go` for helper functions.

## Credentials

- **Username**: `testuser`
- **Password**: `testpass`
- **SSH Key**: Mount public key to `/home/testuser/.ssh/authorized_keys`
- **Sudo**: Full sudo access without password

## Pre-configured Paths

- MongoDB data: `/data/mongodb`
- MongoDB logs: `/var/log/mongodb`
- MongoDB config: `/etc/mongodb`

All paths are owned by `testuser`.
