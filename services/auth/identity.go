package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// ErrEmptyDevice is returned when a device name is required but blank.
var ErrEmptyDevice = errors.New("auth: empty device name")

// Identity is the authenticated caller: a user on one of their devices.
// UserID matches types.User.ID (and the user_id fields of the API); Device
// is the free-form device name given at login; TokenID identifies the bearer
// token that proved this identity.
type Identity struct {
	UserID    string    `json:"user_id"`
	Device    string    `json:"device"`
	TokenID   string    `json:"token_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewIdentity builds a validated Identity. userID and device are required.
func NewIdentity(userID, device, tokenID string, expiresAt time.Time) (Identity, error) {
	if strings.TrimSpace(userID) == "" {
		return Identity{}, types.ErrEmptyUserID
	}
	if strings.TrimSpace(device) == "" {
		return Identity{}, ErrEmptyDevice
	}
	return Identity{
		UserID:    userID,
		Device:    strings.TrimSpace(device),
		TokenID:   tokenID,
		ExpiresAt: expiresAt.UTC(),
	}, nil
}

// Expired reports whether the identity outlived expiresAt.
func (id Identity) Expired(now time.Time) bool {
	return !now.Before(id.ExpiresAt)
}

// identityKey is the context key holding the request Identity.
type identityKey struct{}

// ContextWithIdentity returns a context carrying id.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the Identity carried by ctx, if any.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
