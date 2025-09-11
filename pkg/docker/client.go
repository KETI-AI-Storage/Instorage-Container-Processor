package docker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"go.uber.org/zap"
)

type ContainerStatus string

const (
	ContainerStatusRunning ContainerStatus = "running"
	ContainerStatusExited  ContainerStatus = "exited"
	ContainerStatusError   ContainerStatus = "error"
	ContainerStatusUnknown ContainerStatus = "unknown"
)

type VolumeMount struct {
	HostPath      string `json:"hostPath"`
	ContainerPath string `json:"containerPath"`
	ReadOnly      bool   `json:"readOnly"`
}

type ContainerConfig struct {
	Image     string        `json:"image"`
	Command   []string      `json:"command,omitempty"`
	Args      []string      `json:"args,omitempty"`
	Env       []string      `json:"env,omitempty"`
	Volumes   []VolumeMount `json:"volumes,omitempty"`
	Resources struct {
		CPULimit    int64 `json:"cpuLimit,omitempty"`
		MemoryLimit int64 `json:"memoryLimit,omitempty"`
	} `json:"resources,omitempty"`
}

type Client struct {
	cli    *client.Client
	logger *zap.Logger
}

func NewClient(logger *zap.Logger) (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	
	return &Client{
		cli:    cli,
		logger: logger,
	}, nil
}

func (c *Client) CreateContainer(ctx context.Context, config ContainerConfig) (string, error) {
	var cmd []string
	if len(config.Command) > 0 {
		cmd = append(cmd, config.Command...)
	}
	if len(config.Args) > 0 {
		cmd = append(cmd, config.Args...)
	}
	
	var mounts []mount.Mount
	for _, vol := range config.Volumes {
		mountType := mount.TypeBind
		mounts = append(mounts, mount.Mount{
			Type:     mountType,
			Source:   vol.HostPath,
			Target:   vol.ContainerPath,
			ReadOnly: vol.ReadOnly,
		})
	}
	
	containerConfig := &container.Config{
		Image: config.Image,
		Cmd:   cmd,
		Env:   config.Env,
	}
	
	hostConfig := &container.HostConfig{
		Mounts: mounts,
	}
	
	if config.Resources.CPULimit > 0 {
		hostConfig.Resources.CPUQuota = config.Resources.CPULimit
	}
	if config.Resources.MemoryLimit > 0 {
		hostConfig.Resources.Memory = config.Resources.MemoryLimit
	}
	
	resp, err := c.cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		if client.IsErrNotFound(err) && strings.Contains(err.Error(), "No such image") {
			if err := c.pullImage(ctx, config.Image); err != nil {
				return "", fmt.Errorf("failed to pull image %s: %w", config.Image, err)
			}
			
			resp, err = c.cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
			if err != nil {
				return "", fmt.Errorf("failed to create container after pulling image: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to create container: %w", err)
		}
	}
	
	return resp.ID, nil
}

func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	return nil
}

func (c *Client) StopContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	return nil
}

func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}
	return nil
}

func (c *Client) GetContainerStatus(ctx context.Context, containerID string) (ContainerStatus, error) {
	resp, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return ContainerStatusUnknown, fmt.Errorf("failed to inspect container: %w", err)
	}
	
	switch resp.State.Status {
	case "running":
		return ContainerStatusRunning, nil
	case "exited":
		return ContainerStatusExited, nil
	case "dead", "error":
		return ContainerStatusError, nil
	default:
		return ContainerStatusUnknown, nil
	}
}

func (c *Client) GetContainerExitCode(ctx context.Context, containerID string) (int, error) {
	resp, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return -1, fmt.Errorf("failed to inspect container: %w", err)
	}
	
	return resp.State.ExitCode, nil
}

func (c *Client) GetContainerLogs(ctx context.Context, containerID string) (string, error) {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	}
	
	reader, err := c.cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return "", fmt.Errorf("failed to get container logs: %w", err)
	}
	defer reader.Close()
	
	logs, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read container logs: %w", err)
	}
	
	return string(logs), nil
}

func (c *Client) pullImage(ctx context.Context, image string) error {
	reader, err := c.cli.ImagePull(ctx, image, imagetypes.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	
	_, err = io.ReadAll(reader)
	return err
}

func (c *Client) Close() error {
	return c.cli.Close()
}