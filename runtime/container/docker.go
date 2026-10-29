package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/process"
)

// DockerBackend is a ContainerBackend wrapping the docker CLI via os/exec.
// It needs no SDK and is used only when a daemon is present; see
// DockerAvailable.
type DockerBackend struct {
	bin string
}

var _ ContainerBackend = (*DockerBackend)(nil)

// NewDockerBackend builds a backend from the docker binary found in PATH.
// It does not check the daemon: use DockerAvailable for that.
func NewDockerBackend() (*DockerBackend, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("container: docker binary not found: %w", err)
	}
	return &DockerBackend{bin: bin}, nil
}

// DockerAvailable reports whether a docker daemon answers within timeout.
func DockerAvailable(ctx context.Context) bool {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// run executes a docker command, returning trimmed stdout.
func (d *DockerBackend) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("container: docker %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Create registers a stopped container and returns its id.
func (d *DockerBackend) Create(ctx context.Context, cfg Config) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.Image) == "" {
		return "", fmt.Errorf("container: docker create: image required")
	}
	args := []string{"create"}
	if name := strings.TrimSpace(cfg.Name); name != "" {
		args = append(args, "--name", name)
	}
	if cfg.Dir != "" {
		args = append(args, "--workdir", cfg.Dir)
	}
	for _, e := range cfg.Env {
		args = append(args, "--env", e)
	}
	args = append(args, cfg.Image)
	if strings.TrimSpace(cfg.Command) != "" {
		args = append(args, cfg.Command)
		args = append(args, cfg.Args...)
	}
	return d.run(ctx, args...)
}

// Start boots a created or stopped container.
func (d *DockerBackend) Start(ctx context.Context, id string) error {
	_, err := d.run(ctx, "start", id)
	return err
}

// Stop halts a running container.
func (d *DockerBackend) Stop(ctx context.Context, id string) error {
	_, err := d.run(ctx, "stop", id)
	return err
}

// Remove deletes a container, forcing removal when running.
func (d *DockerBackend) Remove(ctx context.Context, id string) error {
	_, err := d.run(ctx, "rm", "--force", id)
	return err
}

// Exec runs a command in a running container, streaming stdio.
func (d *DockerBackend) Exec(ctx context.Context, id string, opts ExecOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(opts.Cmd) == 0 {
		return fmt.Errorf("container: docker exec %s: empty command", id)
	}
	args := append([]string{"exec", "--interactive", id}, opts.Cmd...)
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container: docker exec %s: %w", id, err)
	}
	return nil
}

// Inspect returns a snapshot of the container metadata.
func (d *DockerBackend) Inspect(ctx context.Context, id string) (Info, error) {
	out, err := d.run(ctx, "inspect", "--format", "{{.Name}}\t{{.State.Status}}", id)
	if err != nil {
		return Info{}, err
	}
	name, status, _ := strings.Cut(out, "\t")
	return Info{ID: id, Name: strings.TrimPrefix(name, "/"), State: dockerState(status)}, nil
}

// dockerState maps a docker status word to a container State.
func dockerState(status string) State {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created":
		return StateCreated
	case "running", "paused", "restarting":
		return StateRunning
	default:
		return StateStopped
	}
}

// Processes returns the container main process from docker inspect. Only
// the main process ("main") is tracked: per-process listing inside the
// container is future work.
func (d *DockerBackend) Processes(ctx context.Context, id string) ([]process.Info, error) {
	out, err := d.run(ctx, "inspect", "--format", "{{.Path}}\t{{.State.Status}}\t{{.State.Pid}}", id)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(out, "\t", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("container: docker processes %s: unexpected inspect output %q", id, out)
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(parts[2]), "%d", &pid)
	state := types.ProcessExited
	if dockerState(parts[1]) == StateRunning {
		state = types.ProcessRunning
	}
	return []process.Info{{
		ID:      MainProcessID,
		Command: parts[0],
		PID:     pid,
		State:   state,
	}}, nil
}

// Logs streams the container logs (docker logs) for the main process.
// Only the "main" process id is supported.
func (d *DockerBackend) Logs(ctx context.Context, id, processID string, follow bool) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if processID != MainProcessID {
		return nil, fmt.Errorf("%w: %s in container %s", ErrProcessNotFound, processID, id)
	}
	args := []string{"logs", id}
	if follow {
		args = []string{"logs", "--follow", id}
	}
	pr, pw := io.Pipe()
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, fmt.Errorf("container: docker logs %s: %w", id, err)
	}
	go func() {
		pw.CloseWithError(cmd.Wait())
	}()
	return pr, nil
}
