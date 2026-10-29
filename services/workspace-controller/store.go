package controller

import (
	"context"
	"errors"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// Errors returned by Store implementations.
var (
	// ErrWorkspaceNotFound is returned when no workspace matches the id.
	ErrWorkspaceNotFound = errors.New("controller: workspace not found")
	// ErrWorkspaceExists is returned when creating a workspace whose id
	// is already taken.
	ErrWorkspaceExists = errors.New("controller: workspace already exists")
)

// Store persists workspaces.
//
// NOTE: the current implementation is a stdlib JSON-file-backed store
// (FileStore). A Postgres implementation comes later; this interface is
// the swap boundary, so keep new persistence needs behind it instead of
// leaking storage details into the Controller.
//
// Implementations must be safe for concurrent use.
type Store interface {
	// Create persists a new workspace. It returns ErrWorkspaceExists
	// (wrapped) when the id is already taken.
	Create(ctx context.Context, ws types.Workspace) error
	// Get returns the workspace with the given id, or
	// ErrWorkspaceNotFound (wrapped) when absent.
	Get(ctx context.Context, id string) (types.Workspace, error)
	// List returns every stored workspace, ordered by id.
	List(ctx context.Context) ([]types.Workspace, error)
	// Update replaces the stored workspace, or returns
	// ErrWorkspaceNotFound (wrapped) when absent.
	Update(ctx context.Context, ws types.Workspace) error
	// Delete removes the workspace, or returns ErrWorkspaceNotFound
	// (wrapped) when absent.
	Delete(ctx context.Context, id string) error
}
