package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
)

// Process event payloads published around Exec commands.
type processStartedPayload struct {
	ProcessID string   `json:"process_id"`
	Command   []string `json:"command"`
}

type processStdoutPayload struct {
	ProcessID string `json:"process_id"`
	Data      string `json:"data"`
}

type processExitedPayload struct {
	ProcessID string `json:"process_id"`
	ExitCode  int    `json:"exit_code"`
	Error     string `json:"error,omitempty"`
}

// generateExecID returns a random exec process id.
func generateExecID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("exec-%d", time.Now().UTC().UnixNano())
	}
	return "exec-" + hex.EncodeToString(b[:])
}

// emitProcess publishes a process-scoped event for the workspace.
func (c *Controller) emitProcess(workspaceID string, typ protocol.EventType, payload any) {
	ev, err := protocol.NewEvent(typ, workspaceID, payload)
	if err != nil {
		return
	}
	c.bus.Publish(ev)
}

// eventWriter forwards command output while publishing a process.stdout
// event per write. Stdout and stderr share the event: it carries the
// combined stream.
type eventWriter struct {
	bus         *protocol.EventBus
	workspaceID string
	processID   string
	dst         io.Writer
}

func (w eventWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		ev, err := protocol.NewEvent(protocol.EventProcessStdout, w.workspaceID,
			processStdoutPayload{ProcessID: w.processID, Data: string(p)})
		if err == nil {
			w.bus.Publish(ev)
		}
	}
	return w.dst.Write(p)
}

// ListProcesses returns the workspace container processes. A workspace
// without a bound container (never started, or container gone from the
// backend) lists as empty.
func (c *Controller) ListProcesses(ctx context.Context, workspaceID string) ([]types.Process, error) {
	ws, err := c.store.Get(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	ctrID, ok := c.containerFor(workspaceID)
	if !ok {
		return []types.Process{}, nil
	}
	infos, err := c.backend.Processes(ctx, ctrID)
	if err != nil {
		if errors.Is(err, container.ErrNotFound) {
			return []types.Process{}, nil
		}
		return nil, err
	}
	out := make([]types.Process, 0, len(infos))
	for _, in := range infos {
		out = append(out, types.Process{
			ID:          in.ID,
			WorkspaceID: ws.ID,
			Command:     in.Command,
			Args:        in.Args,
			PID:         in.PID,
			State:       in.State,
			ExitCode:    in.ExitCode,
			StartedAt:   in.StartedAt,
		})
	}
	return out, nil
}

// ProcessLogs returns the combined output stream of one container
// process. With follow set, reads block for new output until the
// process exits; otherwise buffered output is returned and reads end
// with io.EOF. Unknown processes fail with
// container.ErrProcessNotFound.
func (c *Controller) ProcessLogs(ctx context.Context, workspaceID, processID string, follow bool) (io.ReadCloser, error) {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return nil, err
	}
	ctrID, ok := c.containerFor(workspaceID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", container.ErrProcessNotFound, processID)
	}
	rc, err := c.backend.Logs(ctx, ctrID, processID, follow)
	if err != nil {
		if errors.Is(err, container.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", container.ErrProcessNotFound, processID)
		}
		return nil, err
	}
	return rc, nil
}

// prepareExec resolves the container and assigns the exec process id,
// generating one when the caller did not provide it.
func (c *Controller) prepareExec(ctx context.Context, workspaceID string, opts container.ExecOptions) (types.Workspace, string, container.ExecOptions, error) {
	ws, err := c.store.Get(ctx, workspaceID)
	if err != nil {
		return types.Workspace{}, "", opts, err
	}
	if ws.Status != types.WorkspaceRunning {
		return types.Workspace{}, "", opts, fmt.Errorf("%w: cannot exec in workspace in %s", ErrIllegalTransition, ws.Status)
	}
	ctrID, ok := c.containerFor(workspaceID)
	if !ok {
		return types.Workspace{}, "", opts, fmt.Errorf("controller: exec %s: no container bound", workspaceID)
	}
	if strings.TrimSpace(opts.ExecID) == "" {
		opts.ExecID = generateExecID()
	}
	return ws, ctrID, opts, nil
}
