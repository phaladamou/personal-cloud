package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// Client is a minimal control-plane API client.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient builds a Client for the API at base, sending token as a
// bearer credential when non-empty.
func NewClient(base, token string) *Client {
	return &Client{base: base, token: token, http: http.DefaultClient}
}

// Base returns the API base URL.
func (c *Client) Base() string { return c.base }

// apiError is a failed API call with its HTTP status and message.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("api: %s (status %d)", e.Message, e.Status)
}

// do sends a JSON request and decodes a JSON response into out (when
// non-nil). A non-2xx status becomes an *apiError.
func (c *Client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := http.StatusText(resp.StatusCode)
		var parsed map[string]string
		if json.Unmarshal(raw, &parsed) == nil && parsed["error"] != "" {
			msg = parsed["error"]
		}
		return &apiError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// CreateWorkspace creates a workspace for userID with the given name.
func (c *Client) CreateWorkspace(userID, name string) (types.Workspace, error) {
	var ws types.Workspace
	err := c.do(http.MethodPost, "/v1/workspaces",
		map[string]string{"user_id": userID, "name": name}, &ws)
	return ws, err
}

// ListWorkspaces returns all workspaces.
func (c *Client) ListWorkspaces() ([]types.Workspace, error) {
	var out struct {
		Workspaces []types.Workspace `json:"workspaces"`
	}
	if err := c.do(http.MethodGet, "/v1/workspaces", nil, &out); err != nil {
		return nil, err
	}
	return out.Workspaces, nil
}

// GetWorkspace returns one workspace by id.
func (c *Client) GetWorkspace(id string) (types.Workspace, error) {
	var ws types.Workspace
	err := c.do(http.MethodGet, "/v1/workspaces/"+url.PathEscape(id), nil, &ws)
	return ws, err
}

// WorkspaceAction runs a lifecycle action (start, stop) on a workspace.
func (c *Client) WorkspaceAction(id, action string) (types.Workspace, error) {
	var ws types.Workspace
	err := c.do(http.MethodPost,
		"/v1/workspaces/"+url.PathEscape(id)+"/"+action, nil, &ws)
	return ws, err
}

// DeleteWorkspace deletes a workspace by id.
func (c *Client) DeleteWorkspace(id string) error {
	return c.do(http.MethodDelete, "/v1/workspaces/"+url.PathEscape(id), nil, nil)
}

// CreateSnapshot captures the workspace volume state. An empty volumeID
// snapshots the default volume.
func (c *Client) CreateSnapshot(workspaceID, volumeID, name string) (types.Snapshot, error) {
	var snap types.Snapshot
	body := map[string]string{}
	if volumeID != "" {
		body["volume_id"] = volumeID
	}
	if name != "" {
		body["name"] = name
	}
	var req any = body
	if len(body) == 0 {
		req = nil
	}
	err := c.do(http.MethodPost, "/v1/workspaces/"+url.PathEscape(workspaceID)+"/snapshots", req, &snap)
	return snap, err
}

// ListSnapshots returns every snapshot of the workspace.
func (c *Client) ListSnapshots(workspaceID string) ([]types.Snapshot, error) {
	var out struct {
		Snapshots []types.Snapshot `json:"snapshots"`
	}
	if err := c.do(http.MethodGet, "/v1/workspaces/"+url.PathEscape(workspaceID)+"/snapshots", nil, &out); err != nil {
		return nil, err
	}
	return out.Snapshots, nil
}

// RestoreSnapshot replaces the snapshot volume content with the
// captured state and returns the snapshot.
func (c *Client) RestoreSnapshot(workspaceID, snapshotID string) (types.Snapshot, error) {
	var snap types.Snapshot
	err := c.do(http.MethodPost,
		"/v1/workspaces/"+url.PathEscape(workspaceID)+"/snapshots/"+url.PathEscape(snapshotID)+"/restore",
		nil, &snap)
	return snap, err
}

// DeleteSnapshot deletes a snapshot by id.
func (c *Client) DeleteSnapshot(workspaceID, snapshotID string) error {
	return c.do(http.MethodDelete,
		"/v1/workspaces/"+url.PathEscape(workspaceID)+"/snapshots/"+url.PathEscape(snapshotID), nil, nil)
}
