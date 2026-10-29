package main

import (
	"bytes"
	"context"
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

// maxRawBytes bounds raw (non-JSON) response bodies.
const maxRawBytes = 16 << 20

// doRaw sends a raw request and returns the response body. A non-2xx
// status becomes an *apiError.
func (c *Client) doRaw(method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRawBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := http.StatusText(resp.StatusCode)
		var parsed map[string]string
		if json.Unmarshal(raw, &parsed) == nil && parsed["error"] != "" {
			msg = parsed["error"]
		}
		return nil, &apiError{Status: resp.StatusCode, Message: msg}
	}
	return raw, nil
}

// doStream sends a request and copies the response body into w. A
// non-2xx status becomes an *apiError.
func (c *Client) doStream(ctx context.Context, method, path string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		msg := http.StatusText(resp.StatusCode)
		var parsed map[string]string
		if json.Unmarshal(raw, &parsed) == nil && parsed["error"] != "" {
			msg = parsed["error"]
		}
		return &apiError{Status: resp.StatusCode, Message: msg}
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("stream response: %w", err)
	}
	return nil
}

// filesPath builds a /files URL with an optional path query parameter.
func filesPath(workspaceID, op, path string) string {
	u := "/v1/workspaces/" + url.PathEscape(workspaceID) + "/files" + op
	if path != "" {
		u += "?path=" + url.QueryEscape(path)
	}
	return u
}

// ListFiles returns the entries of the workspace directory at dir.
// Empty dir denotes the workspace root.
func (c *Client) ListFiles(workspaceID, dir string) ([]types.FileEntry, error) {
	var out struct {
		Files []types.FileEntry `json:"files"`
	}
	if err := c.do(http.MethodGet, filesPath(workspaceID, "", dir), nil, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// ReadFile returns the content of path.
func (c *Client) ReadFile(workspaceID, path string) ([]byte, error) {
	return c.doRaw(http.MethodGet, filesPath(workspaceID, "/content", path), nil)
}

// WriteFile stores data at path.
func (c *Client) WriteFile(workspaceID, path string, data []byte) error {
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	_, err := c.doRaw(http.MethodPut, filesPath(workspaceID, "/content", path), body)
	return err
}

// Mkdir creates the directory at path with its parents.
func (c *Client) Mkdir(workspaceID, path string) error {
	return c.do(http.MethodPost, "/v1/workspaces/"+url.PathEscape(workspaceID)+"/files/mkdir",
		map[string]string{"path": path}, nil)
}

// DeleteFile removes the file or directory at path.
func (c *Client) DeleteFile(workspaceID, path string) error {
	_, err := c.doRaw(http.MethodDelete, filesPath(workspaceID, "", path), nil)
	return err
}

// ListProcesses returns the workspace container processes.
func (c *Client) ListProcesses(workspaceID string) ([]types.Process, error) {
	var out struct {
		Processes []types.Process `json:"processes"`
	}
	if err := c.do(http.MethodGet, "/v1/workspaces/"+url.PathEscape(workspaceID)+"/processes", nil, &out); err != nil {
		return nil, err
	}
	return out.Processes, nil
}

// ProcessLogs copies the process output into w. With follow set, it
// streams until the process exits; cancel ctx to stop early.
func (c *Client) ProcessLogs(ctx context.Context, workspaceID, processID string, follow bool, w io.Writer) error {
	path := "/v1/workspaces/" + url.PathEscape(workspaceID) +
		"/processes/" + url.PathEscape(processID) + "/logs"
	if follow {
		path += "?follow=true"
	}
	return c.doStream(ctx, http.MethodGet, path, w)
}
