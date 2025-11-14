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

// Client provides Docker operations
type Client struct{}

// NewClient creates a new Docker client
func NewClient() *Client {
	return &Client{}
}

// CheckDockerInstalled verifies Docker is installed and accessible
func (c *Client) CheckDockerInstalled(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker not installed or not accessible: %w\nOutput: %s", err, output)
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
	cmd := exec.CommandContext(ctx, "docker", "pull", imageRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w\nOutput: %s", imageRef, err, output)
	}

	return nil
}

// ImageExists checks if a Docker image exists locally
func (c *Client) ImageExists(ctx context.Context, image, tag string) (bool, error) {
	imageRef := fmt.Sprintf("%s:%s", image, tag)
	cmd := exec.CommandContext(ctx, "docker", "images", "-q", imageRef)
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check image: %w", err)
	}

	return len(strings.TrimSpace(string(output))) > 0, nil
}

// ContainerRunning checks if a container is running
func (c *Client) ContainerRunning(ctx context.Context, containerName string) (bool, error) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-q", "-f", fmt.Sprintf("name=%s", containerName))
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check container: %w", err)
	}

	return len(strings.TrimSpace(string(output))) > 0, nil
}

// GetContainerID gets the container ID by name
func (c *Client) GetContainerID(ctx context.Context, containerName string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "-f", fmt.Sprintf("name=%s", containerName))
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
	cmd := exec.CommandContext(ctx, "docker", "stop", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w\nOutput: %s", containerName, err, output)
	}
	return nil
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore error if container doesn't exist
		if !strings.Contains(string(output), "No such container") {
			return fmt.Errorf("failed to remove container %s: %w\nOutput: %s", containerName, err, output)
		}
	}
	return nil
}
