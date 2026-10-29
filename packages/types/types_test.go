package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNewUser(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		email   string
		wantErr bool
	}{
		{"valid", "u-1", "ada@example.com", false},
		{"empty id", "", "ada@example.com", true},
		{"blank id", "   ", "ada@example.com", true},
		{"empty email", "u-1", "", true},
		{"missing at", "u-1", "adaexample.com", true},
		{"missing domain dot", "u-1", "ada@example", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := NewUser(tt.id, tt.email, "Ada")
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.wantErr && u.ID != tt.id {
				t.Fatalf("got id %q, want %q", u.ID, tt.id)
			}
		})
	}
}

func TestUserString(t *testing.T) {
	if got := (User{Email: "a@b.co", Name: "Al"}).String(); got != "Al <a@b.co>" {
		t.Fatalf("got %q", got)
	}
	if got := (User{Email: "a@b.co"}).String(); got != "a@b.co" {
		t.Fatalf("got %q", got)
	}
}

func TestNewWorkspace(t *testing.T) {
	tests := []struct {
		name       string
		id, userID string
		wsName     string
		wantErr    bool
	}{
		{"valid", "w-1", "u-1", "dev", false},
		{"empty id", "", "u-1", "dev", true},
		{"empty user", "w-1", "", "dev", true},
		{"empty name", "w-1", "u-1", "  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := NewWorkspace(tt.id, tt.userID, tt.wsName)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if w.Status != WorkspaceCreated {
					t.Fatalf("got status %s, want CREATED", w.Status)
				}
			}
		})
	}
}

func TestWorkspacePlacementFields(t *testing.T) {
	w, err := NewWorkspace("w-1", "u-1", "dev")
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if w.WantsPlacement() {
		t.Fatal("fresh workspace wants placement, want none")
	}
	w.Resources = Resource{CPU: 2, MemoryMB: 1024, DiskGB: 10, GPUs: 1}
	w.Selector = map[string]string{"zone": "east"}
	if !w.WantsPlacement() {
		t.Fatal("workspace with requirements wants no placement, want some")
	}
	// Stores written before the placement fields decode to a
	// zero-requirement workspace: no placement, old behavior.
	var old Workspace
	if err := json.Unmarshal([]byte(`{"id":"w-1","user_id":"u-1","name":"dev","status":"CREATED"}`), &old); err != nil {
		t.Fatalf("decode old workspace: %v", err)
	}
	if old.WantsPlacement() || old.NodeID != "" {
		t.Fatalf("old workspace: got %+v, want no placement", old)
	}
}

func TestWorkspaceTransitions(t *testing.T) {
	tests := []struct {
		from    WorkspaceStatus
		to      WorkspaceStatus
		wantErr bool
	}{
		{WorkspaceCreated, WorkspaceStarting, false},
		{WorkspaceCreated, WorkspaceRunning, true},
		{WorkspaceStarting, WorkspaceRunning, false},
		{WorkspaceStarting, WorkspaceStopped, false},
		{WorkspaceStarting, WorkspaceSleeping, true},
		{WorkspaceRunning, WorkspaceSleeping, false},
		{WorkspaceRunning, WorkspaceStopped, false},
		{WorkspaceRunning, WorkspaceStarting, true},
		{WorkspaceSleeping, WorkspaceRunning, false},
		{WorkspaceSleeping, WorkspaceStopped, false},
		{WorkspaceSleeping, WorkspaceCreated, true},
		{WorkspaceStopped, WorkspaceStarting, false},
		{WorkspaceStopped, WorkspaceRestoring, false},
		{WorkspaceStopped, WorkspaceRunning, true},
		{WorkspaceRestoring, WorkspaceRunning, false},
		{WorkspaceRestoring, WorkspaceStopped, false},
		{WorkspaceRestoring, WorkspaceSleeping, true},
		{WorkspaceRunning, WorkspaceStatus("BOGUS"), true},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			w := Workspace{ID: "w-1", Status: tt.from}
			err := w.TransitionTo(tt.to)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if w.Status != tt.to {
					t.Fatalf("got status %s, want %s", w.Status, tt.to)
				}
			}
		})
	}
}

func TestWorkspaceStatusValid(t *testing.T) {
	for _, s := range []WorkspaceStatus{
		WorkspaceCreated, WorkspaceStarting, WorkspaceRunning,
		WorkspaceSleeping, WorkspaceStopped, WorkspaceRestoring,
	} {
		if !s.Valid() {
			t.Fatalf("status %s should be valid", s)
		}
	}
	if (WorkspaceStatus("NOPE")).Valid() {
		t.Fatalf("bogus status should be invalid")
	}
}

func TestNewSession(t *testing.T) {
	tests := []struct {
		name                    string
		id, workspaceID, userID string
		wantErr                 bool
	}{
		{"valid", "s-1", "w-1", "u-1", false},
		{"empty id", "", "w-1", "u-1", true},
		{"empty workspace", "s-1", "", "u-1", true},
		{"empty user", "s-1", "w-1", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSession(tt.id, tt.workspaceID, tt.userID, "laptop")
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewNode(t *testing.T) {
	tests := []struct {
		name         string
		id, nodeName string
		cpu          int
		mem, disk    int64
		gpus         int
		wantErr      bool
	}{
		{"valid", "n-1", "gpu-box", 8, 32768, 512, 1, false},
		{"no gpu ok", "n-1", "box", 4, 16384, 256, 0, false},
		{"empty id", "", "box", 4, 16384, 256, 0, true},
		{"empty name", "n-1", "", 4, 16384, 256, 0, true},
		{"zero cpu", "n-1", "box", 0, 16384, 256, 0, true},
		{"zero mem", "n-1", "box", 4, 0, 256, 0, true},
		{"zero disk", "n-1", "box", 4, 16384, 0, 0, true},
		{"negative gpu", "n-1", "box", 4, 16384, 256, -1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := NewNode(tt.id, tt.nodeName, tt.cpu, tt.mem, tt.disk, tt.gpus)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if n.Status != NodeOffline {
					t.Fatalf("got status %s, want OFFLINE", n.Status)
				}
			}
		})
	}
}

func TestNewResource(t *testing.T) {
	if _, err := NewResource(2, 4096, 50, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, args := range [][4]int{{0, 4096, 50, 0}, {2, 0, 50, 0}, {2, 4096, 0, 0}, {2, 4096, 50, -1}} {
		if _, err := NewResource(args[0], int64(args[1]), int64(args[2]), args[3]); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestResourceFits(t *testing.T) {
	cap, _ := NewResource(8, 32768, 500, 1)
	tests := []struct {
		name string
		req  Resource
		want bool
	}{
		{"fits", Resource{CPU: 2, MemoryMB: 4096, DiskGB: 50}, true},
		{"exact", Resource{CPU: 8, MemoryMB: 32768, DiskGB: 500, GPUs: 1}, true},
		{"too much cpu", Resource{CPU: 16, MemoryMB: 4096, DiskGB: 50}, false},
		{"too much mem", Resource{CPU: 2, MemoryMB: 65536, DiskGB: 50}, false},
		{"too much disk", Resource{CPU: 2, MemoryMB: 4096, DiskGB: 1000}, false},
		{"too many gpus", Resource{CPU: 2, MemoryMB: 4096, DiskGB: 50, GPUs: 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.req.Fits(cap); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewProcess(t *testing.T) {
	p, err := NewProcess("p-1", "w-1", "bash", []string{"-lc", "echo hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.State != ProcessRunning {
		t.Fatalf("got state %s, want RUNNING", p.State)
	}
	for name, args := range map[string][3]string{
		"empty id":        {"", "w-1", "bash"},
		"empty workspace": {"p-1", "", "bash"},
		"empty command":   {"p-1", "w-1", "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewProcess(args[0], args[1], args[2], nil); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestNewVolume(t *testing.T) {
	tests := []struct {
		name    string
		sizeGB  int64
		mount   string
		wantErr bool
	}{
		{"valid", 100, "/data", false},
		{"zero size", 0, "/data", true},
		{"negative size", -5, "/data", true},
		{"relative mount", 100, "data", true},
		{"empty mount", 100, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewVolume("v-1", "w-1", "data", tt.sizeGB, tt.mount)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	if _, err := NewVolume("", "w-1", "data", 100, "/data"); err == nil {
		t.Fatalf("expected error for empty id")
	}
	if _, err := NewVolume("v-1", "", "data", 100, "/data"); err == nil {
		t.Fatalf("expected error for empty workspace")
	}
	if _, err := NewVolume("v-1", "w-1", "", 100, "/data"); err == nil {
		t.Fatalf("expected error for empty name")
	}
}

func TestNewSnapshot(t *testing.T) {
	s, err := NewSnapshot("s-1", "w-1", "v-1", "nightly")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.VolumeID != "v-1" || s.Name != "nightly" {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	for name, args := range map[string][3]string{
		"empty id":        {"", "w-1", "v-1"},
		"empty workspace": {"s-1", "", "v-1"},
		"empty volume":    {"s-1", "w-1", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSnapshot(args[0], args[1], args[2], ""); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestNewJob(t *testing.T) {
	j, err := NewJob("j-1", "w-1", "snapshot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j.Status != JobPending {
		t.Fatalf("got status %s, want PENDING", j.Status)
	}
	for name, args := range map[string][3]string{
		"empty id":        {"", "w-1", "snapshot"},
		"empty workspace": {"j-1", "", "snapshot"},
		"empty kind":      {"j-1", "w-1", "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewJob(args[0], args[1], args[2]); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	w, _ := NewWorkspace("w-1", "u-1", "dev")
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"id"`, `"user_id"`, `"name"`, `"status"`, `"created_at"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing key %s in %s", key, raw)
		}
	}
	var back Workspace
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, w) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", back, w)
	}
}

func TestEnumStrings(t *testing.T) {
	if WorkspaceRunning.String() != "RUNNING" {
		t.Fatalf("bad workspace status string")
	}
	if NodeOnline.String() != "ONLINE" {
		t.Fatalf("bad node status string")
	}
	if ProcessExited.String() != "EXITED" {
		t.Fatalf("bad process state string")
	}
	if JobFailed.String() != "FAILED" {
		t.Fatalf("bad job status string")
	}
	if (NodeStatus("X")).Valid() || (ProcessState("X")).Valid() || (JobStatus("X")).Valid() {
		t.Fatalf("bogus enum values should be invalid")
	}
}

func TestNewFileEntry(t *testing.T) {
	e, err := NewFileEntry("docs/readme.md", "readme.md", false, 12, nowUTC())
	if err != nil {
		t.Fatalf("NewFileEntry: %v", err)
	}
	if e.Path != "docs/readme.md" || e.Name != "readme.md" || e.IsDir || e.Size != 12 {
		t.Fatalf("NewFileEntry fields: got %+v", e)
	}
	if got := e.String(); !strings.Contains(got, "docs/readme.md") {
		t.Fatalf("String: got %q", got)
	}
	d, err := NewFileEntry("docs", "docs", true, 0, nowUTC())
	if err != nil {
		t.Fatalf("NewFileEntry dir: %v", err)
	}
	if !d.IsDir || !strings.HasSuffix(d.String(), "(dir)") {
		t.Fatalf("dir entry: got %+v / %q", d, d.String())
	}
	for name, args := range map[string]struct {
		path, entry string
		size        int64
	}{
		"empty path": {path: "", entry: "x"},
		"blank path": {path: "  ", entry: "x"},
		"empty name": {path: "x", entry: ""},
		"neg size":   {path: "x", entry: "x", size: -1},
	} {
		if _, err := NewFileEntry(args.path, args.entry, false, args.size, nowUTC()); err == nil {
			t.Fatalf("%s: got nil error", name)
		}
	}
}
