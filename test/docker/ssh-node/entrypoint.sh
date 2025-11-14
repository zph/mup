#!/bin/bash
set -e

# Function to wait for authorized_keys file
wait_for_ssh_key() {
    echo "Checking for SSH key..."
    local max_wait=3  # Reduced from 30 to 3 seconds
    local waited=0

    while [ ! -f /home/testuser/.ssh/authorized_keys ] && [ $waited -lt $max_wait ]; do
        sleep 1
        waited=$((waited + 1))
    done

    if [ -f /home/testuser/.ssh/authorized_keys ]; then
        echo "SSH key found, setting permissions..."
        chown testuser:testuser /home/testuser/.ssh/authorized_keys
        chmod 600 /home/testuser/.ssh/authorized_keys
        return 0
    else
        echo "No SSH key mounted (using password authentication)"
        return 1
    fi
}

# Ensure /tmp is writable (important for tests)
echo "Ensuring /tmp is writable..."
# Note: In Docker containers, /tmp might have special handling
# Just verify it's writable, don't try to chmod it
if ! touch /tmp/.test-write 2>/dev/null; then
    echo "WARNING: /tmp is not writable, tests might fail"
else
    rm -f /tmp/.test-write
    echo "/tmp is writable"
fi

# Generate host keys if they don't exist
ssh-keygen -A

# Wait for SSH key (optional, will fall back to password)
wait_for_ssh_key || true

# Start SSH daemon in foreground
echo "Starting SSH daemon..."
exec /usr/sbin/sshd -D -e
