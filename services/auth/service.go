package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// Token errors. They deliberately carry no token material: callers may
// surface them to clients and logs alike.
var (
	// ErrInvalidToken marks a malformed, forged, unknown or revoked token.
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrExpiredToken marks a well-formed token past its expiry.
	ErrExpiredToken = errors.New("auth: expired token")
)

// Token format constants (see doc.go).
const (
	tokenVersion = "pc1"
	tokenIDBytes = 16
	// minSecretBytes floors the HMAC secret at 128 bits.
	minSecretBytes = 16
)

// DefaultTTL is the default token lifetime: device tokens are long-lived
// (a personal cloud trusts its own devices) but still expire.
const DefaultTTL = 90 * 24 * time.Hour

var encoding = base64.RawURLEncoding

// Service issues, validates and revokes bearer tokens. It is stdlib-only
// (HMAC-SHA256 over crypto/rand ids) and safe for concurrent use.
type Service struct {
	store  Store
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithTTL overrides the token lifetime applied at Issue.
func WithTTL(d time.Duration) Option {
	return func(s *Service) { s.ttl = d }
}

// WithNow overrides the clock (used by tests to simulate expiry).
func WithNow(fn func() time.Time) Option {
	return func(s *Service) { s.now = fn }
}

// NewService builds a Service over store, signing tokens with secret.
// The secret needs at least 16 bytes; pass a 32-byte random value in
// production. The secret is copied, so callers may wipe their buffer.
func NewService(store Store, secret []byte, opts ...Option) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("auth: nil store")
	}
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("auth: secret must be at least %d bytes", minSecretBytes)
	}
	s := &Service{store: store, secret: append([]byte(nil), secret...), ttl: DefaultTTL, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	if s.ttl <= 0 {
		return nil, fmt.Errorf("auth: TTL must be positive")
	}
	return s, nil
}

// sign returns HMAC-SHA256(secret, id).
func (s *Service) sign(id []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(id)
	return mac.Sum(nil)
}

// Issue mints a fresh bearer token for userID on device and persists its
// record. Every call yields a distinct token, so each device holds its own
// revocable credential.
func (s *Service) Issue(ctx context.Context, userID, device string) (token string, id Identity, err error) {
	if strings.TrimSpace(userID) == "" {
		return "", Identity{}, types.ErrEmptyUserID
	}
	if strings.TrimSpace(device) == "" {
		return "", Identity{}, ErrEmptyDevice
	}
	var raw [tokenIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Identity{}, fmt.Errorf("auth: random token id: %w", err)
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	key := encoding.EncodeToString(raw[:])
	rec := Record{
		ID:        key,
		UserID:    userID,
		Device:    strings.TrimSpace(device),
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	if err := s.store.Save(ctx, rec); err != nil {
		return "", Identity{}, fmt.Errorf("auth: save token: %w", err)
	}
	return tokenVersion + "." + key + "." + encoding.EncodeToString(s.sign(raw[:])),
		Identity{UserID: userID, Device: rec.Device, TokenID: key, ExpiresAt: rec.ExpiresAt}, nil
}

// parseKey verifies the token envelope and signature, returning the record
// key. Anything malformed or forged collapses to ErrInvalidToken.
func (s *Service) parseKey(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenVersion || parts[1] == "" || parts[2] == "" {
		return "", ErrInvalidToken
	}
	raw, err := encoding.DecodeString(parts[1])
	if err != nil || len(raw) != tokenIDBytes {
		return "", ErrInvalidToken
	}
	sig, err := encoding.DecodeString(parts[2])
	if err != nil || len(sig) != sha256.Size {
		return "", ErrInvalidToken
	}
	if subtle.ConstantTimeCompare(sig, s.sign(raw)) != 1 {
		return "", ErrInvalidToken
	}
	return parts[1], nil
}

// Validate authenticates a bearer token, returning the caller identity.
// Unknown or revoked tokens fail with ErrInvalidToken; a well-formed but
// lapsed token fails with ErrExpiredToken and is deleted best-effort.
func (s *Service) Validate(ctx context.Context, token string) (Identity, error) {
	key, err := s.parseKey(token)
	if err != nil {
		return Identity{}, err
	}
	rec, err := s.store.Load(ctx, key)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return Identity{}, ErrInvalidToken
		}
		return Identity{}, fmt.Errorf("auth: load token: %w", err)
	}
	if rec.Expired(s.now()) {
		_ = s.store.Delete(ctx, key) // Best-effort cleanup.
		return Identity{}, ErrExpiredToken
	}
	return Identity{UserID: rec.UserID, Device: rec.Device, TokenID: rec.ID, ExpiresAt: rec.ExpiresAt}, nil
}

// Revoke kills the token, returning nil when it is already gone
// (revocation is idempotent). Malformed tokens fail with ErrInvalidToken.
func (s *Service) Revoke(ctx context.Context, token string) error {
	key, err := s.parseKey(token)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("auth: revoke token: %w", err)
	}
	return nil
}
