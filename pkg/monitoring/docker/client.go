package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const (
	VictoriaMetricsImage = "victoriametrics/victoria-metrics"
	VictoriaMetricsTag   = "latest"
	GrafanaImage         = "grafana/grafana"
	GrafanaTag           = "latest"
)

// Client provides container runtime operations (Docker or Podman)
type Client struct {
	runtime string // "docker" or "podman"
}

// NewClient creates a new container runtime client
// Auto-detects Docker or Podman availability
func NewClient() *Client {
	// Try docker first
	cmd := exec.Command("docker", "version")
	if err := cmd.Run(); err == nil {
		return &Client{runtime: "docker"}
	}

	// Try podman
	cmd = exec.Command("podman", "version")
	if err := cmd.Run(); err == nil {
		return &Client{runtime: "podman"}
	}

	// Default to docker (will fail with helpful error later)
	return &Client{runtime: "docker"}
}

// CheckDockerInstalled verifies container runtime is installed and accessible
func (c *Client) CheckDockerInstalled(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.runtime, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s not installed or not accessible: %w\nOutput: %s", c.runtime, err, output)
	}
	return nil
}

// PullImage pulls a Docker image if not already present
func (c *Client) PullImage(ctx context.Context, image, tag string) error {
	// Check if image exists
	exists, err := c.ImageExists(ctx, image, tag)
	if err != nil {
		return fmt.Errorf("failed to check if image exists: %w", err)
	}

	if exists {
		return nil // Already have the image
	}

	// Pull the image
	imageRef := fmt.Sprintf("%s:%s", image, tag)
	cmd := exec.CommandContext(ctx, c.runtime, "pull", imageRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w\nOutput: %s", imageRef, err, output)
	}

	return nil
}

// ImageExists checks if a container image exists locally
func (c *Client) ImageExists(ctx context.Context, image, tag string) (bool, error) {
	imageRef := fmt.Sprintf("%s:%s", image, tag)
	// Use inspect command which is more reliable across Docker versions
	cmd := exec.CommandContext(ctx, c.runtime, "inspect", "--type=image", imageRef)
	err := cmd.Run()
	if err != nil {
		// inspect returns non-zero exit if image doesn't exist
		return false, nil
	}
	return true, nil
}

// ContainerRunning checks if a container is running
func (c *Client) ContainerRunning(ctx context.Context, containerName string) (bool, error) {
	cmd := exec.CommandContext(ctx, c.runtime, "ps", "-q", "-f", fmt.Sprintf("name=%s", containerName))
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check container: %w", err)
	}

	return len(strings.TrimSpace(string(output))) > 0, nil
}

// GetContainerID gets the container ID by name
func (c *Client) GetContainerID(ctx context.Context, containerName string) (string, error) {
	cmd := exec.CommandContext(ctx, c.runtime, "ps", "-a", "-q", "-f", fmt.Sprintf("name=%s", containerName))
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get container ID: %w", err)
	}

	id := strings.TrimSpace(string(output))
	if id == "" {
		return "", fmt.Errorf("container %s not found", containerName)
	}

	return id, nil
}

// StopContainer stops a running container
func (c *Client) StopContainer(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, c.runtime, "stop", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w\nOutput: %s", containerName, err, output)
	}
	return nil
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, c.runtime, "rm", "-f", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore error if container doesn't exist
		if !strings.Contains(string(output), "No such container") {
			return fmt.Errorf("failed to remove container %s: %w\nOutput: %s", containerName, err, output)
		}
	}
	return nil
}

// GetRuntime returns the detected container runtime name
func (c *Client) GetRuntime() string {
	return c.runtime
}
