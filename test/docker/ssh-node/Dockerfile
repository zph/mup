FROM ubuntu:22.04

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# Install SSH server and common utilities needed for MongoDB deployment
RUN apt-get update && \
    apt-get install -y \
    openssh-server \
    sudo \
    curl \
    wget \
    ca-certificates \
    net-tools \
    lsof \
    procps \
    vim \
    netcat \
    iproute2 \
    && rm -rf /var/lib/apt/lists/*

# Create SSH directory
RUN mkdir -p /var/run/sshd

# Create test user with sudo privileges
RUN useradd -m -s /bin/bash -G sudo testuser && \
    echo 'testuser:testpass' | chpasswd && \
    echo 'testuser ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

# Create .ssh directory for test user
RUN mkdir -p /home/testuser/.ssh && \
    chmod 700 /home/testuser/.ssh && \
    chown testuser:testuser /home/testuser/.ssh

# Configure SSH for better testing
RUN sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#AuthorizedKeysFile/AuthorizedKeysFile/' /etc/ssh/sshd_config && \
    sed -i 's/UsePAM yes/UsePAM no/' /etc/ssh/sshd_config

# Create directory structure for MongoDB data
RUN mkdir -p /data/mongodb /var/log/mongodb /etc/mongodb && \
    chown -R testuser:testuser /data/mongodb /var/log/mongodb /etc/mongodb

# Expose SSH port
EXPOSE 22

# Create entrypoint script
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
