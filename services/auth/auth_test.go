package auth

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// testSecret is a fixed 32-byte HMAC secret for tests.
var testSecret = []byte("test-secret-0123456789-01234567")

func newTestService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	svc, err := NewService(NewFakeStore(), testSecret, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// noLeak fails when err carries token material (tokens and secrets must
// never surface in errors).
func noLeak(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		return
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(err.Error(), s) {
			t.Fatalf("error leaks secret material: %q", err)
		}
	}
}

func TestNewServiceValidation(t *testing.T) {
	if _, err := NewService(nil, testSecret); err == nil {
		t.Fatal("nil store: got nil error, want one")
	}
	for _, secret := range [][]byte{nil, {}, []byte("short")} {
		if _, err := NewService(NewFakeStore(), secret); err == nil {
			t.Fatalf("secret %q: got nil error, want one", secret)
		}
	}
	if _, err := NewService(NewFakeStore(), testSecret, WithTTL(0)); err == nil {
		t.Fatal("zero TTL: got nil error, want one")
	}
	if _, err := NewService(NewFakeStore(), testSecret, WithTTL(-time.Hour)); err == nil {
		t.Fatal("negative TTL: got nil error, want one")
	}
}

func TestIssueValidateRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	token, issued, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !strings.HasPrefix(token, "pc1.") || strings.Count(token, ".") != 2 {
		t.Fatalf("token format: got %q, want pc1.<id>.<sig>", token)
	}
	got, err := svc.Validate(ctx, token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got != issued {
		t.Fatalf("identity: got %+v, want %+v", got, issued)
	}
	if got.UserID != "user-1" || got.Device != "laptop" || got.TokenID == "" {
		t.Fatalf("identity fields: got %+v", got)
	}
	if time.Until(got.ExpiresAt) <= 0 {
		t.Fatalf("expiry: got %v, want a future time", got.ExpiresAt)
	}
}

func TestIssueValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	if _, _, err := svc.Issue(ctx, " ", "laptop"); err == nil {
		t.Fatal("blank user: got nil error, want one")
	}
	if _, _, err := svc.Issue(ctx, "user-1", "  "); err == nil {
		t.Fatal("blank device: got nil error, want one")
	}
}

func TestPerDeviceTokens(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	first, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if first == second {
		t.Fatal("two logins on one device minted the same token")
	}
	phone, _, err := svc.Issue(ctx, "user-1", "phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Revoking one device token leaves the others valid.
	if err := svc.Revoke(ctx, phone); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	for _, tok := range []string{first, second} {
		if _, err := svc.Validate(ctx, tok); err != nil {
			t.Fatalf("Validate surviving token: %v", err)
		}
	}
	if _, err := svc.Validate(ctx, phone); err == nil {
		t.Fatal("Validate revoked token: got nil error, want one")
	}
}

func TestValidateRejects(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	token, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Tamper with the signature (last char still valid base64url).
	tampered := token[:len(token)-1] + "A"
	if tampered == token {
		tampered = token[:len(token)-1] + "B"
	}
	// Tamper with the id: flip one char of the middle part.
	parts := strings.Split(token, ".")
	idTampered := parts[0] + "." + "A" + parts[1][1:] + "." + parts[2]

	for name, bad := range map[string]string{
		"empty":       "",
		"garbage":     "not-a-token",
		"bad version": "pc9." + parts[1] + "." + parts[2],
		"short":       "pc1." + parts[1],
		"bad id":      idTampered,
		"bad sig":     tampered,
		"bad padding": token + "=",
	} {
		err := func() error {
			_, err := svc.Validate(ctx, bad)
			return err
		}()
		if err == nil {
			t.Fatalf("Validate %s: got nil error, want one", name)
		}
		noLeak(t, err, bad, token, string(testSecret))
	}

	// A token signed by another secret fails even though well-formed.
	other, err := NewService(NewFakeStore(), []byte("another-secret-0123456789-01"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	foreign, _, err := other.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue foreign: %v", err)
	}
	if _, err := svc.Validate(ctx, foreign); err == nil {
		t.Fatal("Validate foreign-secret token: got nil error, want one")
	}

	// A well-signed id with no record (other store, same secret) fails.
	lonely, err := NewService(NewFakeStore(), testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	orphan, _, err := lonely.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue orphan: %v", err)
	}
	if _, err := svc.Validate(ctx, orphan); err == nil {
		t.Fatal("Validate recordless token: got nil error, want one")
	}
}

func TestExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	current := now
	svc := newTestService(t, WithTTL(time.Hour), WithNow(func() time.Time { return current }))

	token, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := svc.Validate(ctx, token); err != nil {
		t.Fatalf("Validate fresh: %v", err)
	}
	current = now.Add(2 * time.Hour)
	if _, err := svc.Validate(ctx, token); err != ErrExpiredToken {
		t.Fatalf("Validate expired: got %v, want ErrExpiredToken", err)
	}
	// The expired record is cleaned up: even time travel cannot revive it.
	current = now
	if _, err := svc.Validate(ctx, token); err != ErrInvalidToken {
		t.Fatalf("Validate cleaned-up token: got %v, want ErrInvalidToken", err)
	}
}

func TestRevoke(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	token, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := svc.Revoke(ctx, token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Validate(ctx, token); err != ErrInvalidToken {
		t.Fatalf("Validate revoked: got %v, want ErrInvalidToken", err)
	}
	// Revocation is idempotent.
	if err := svc.Revoke(ctx, token); err != nil {
		t.Fatalf("Revoke again: %v", err)
	}
	if err := svc.Revoke(ctx, "garbage"); err != ErrInvalidToken {
		t.Fatalf("Revoke garbage: got %v, want ErrInvalidToken", err)
	}
}

func TestIdentityHelpers(t *testing.T) {
	if _, err := NewIdentity("", "laptop", "tid", time.Now()); err != types.ErrEmptyUserID {
		t.Fatalf("blank user: got %v, want ErrEmptyUserID", err)
	}
	if _, err := NewIdentity("user-1", " ", "tid", time.Now()); err != ErrEmptyDevice {
		t.Fatalf("blank device: got %v, want ErrEmptyDevice", err)
	}
	exp := time.Now().Add(time.Hour)
	id, err := NewIdentity("user-1", " laptop ", "tid", exp)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if id.Device != "laptop" {
		t.Fatalf("device: got %q, want trimmed", id.Device)
	}
	if id.Expired(time.Now()) || !id.Expired(exp.Add(time.Second)) {
		t.Fatal("Expired disagrees with the clock")
	}
	ctx := ContextWithIdentity(context.Background(), id)
	got, ok := IdentityFrom(ctx)
	if !ok || got != id {
		t.Fatalf("context round trip: got %+v, %v", got, ok)
	}
	if _, ok := IdentityFrom(context.Background()); ok {
		t.Fatal("empty context: got identity, want none")
	}
}

func TestSignatureIsHMACSHA256(t *testing.T) {
	// Pin the documented format: the third part is HMAC-SHA256(secret, id).
	ctx := context.Background()
	svc := newTestService(t)
	token, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(token, ".")
	raw, err := encoding.DecodeString(parts[1])
	if err != nil || len(raw) != tokenIDBytes {
		t.Fatalf("token id: got %d bytes, %v", len(raw), err)
	}
	sig, err := encoding.DecodeString(parts[2])
	if err != nil || len(sig) != sha256.Size {
		t.Fatalf("signature: got %d bytes, %v", len(sig), err)
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "tokens.json") // Missing dir is fine.

	svc, err := NewService(mustFileStore(t, path), testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	token, _, err := svc.Issue(ctx, "user-1", "laptop")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Reopen: the record survived on disk and still validates.
	reopened, err := NewService(mustFileStore(t, path), testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := reopened.Validate(ctx, token)
	if err != nil {
		t.Fatalf("Validate after reopen: %v", err)
	}
	if got.UserID != "user-1" || got.Device != "laptop" {
		t.Fatalf("identity after reopen: got %+v", got)
	}

	// Revocation persists too.
	if err := reopened.Revoke(ctx, token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	third, err := NewService(mustFileStore(t, path), testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := third.Validate(ctx, token); err != ErrInvalidToken {
		t.Fatalf("Validate after revoke+reopen: got %v, want ErrInvalidToken", err)
	}
}

func TestFileStoreErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Corrupt file.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	if _, err := NewFileStore(bad); err == nil {
		t.Fatal("corrupt store: got nil error, want one")
	}

	// Version mismatch.
	vers := filepath.Join(dir, "vers.json")
	if err := os.WriteFile(vers, []byte(`{"version":99,"tokens":{}}`), 0o644); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := NewFileStore(vers); err == nil {
		t.Fatal("version mismatch: got nil error, want one")
	}

	// Missing record.
	store := mustFileStore(t, filepath.Join(dir, "empty.json"))
	if _, err := store.Load(ctx, "nope"); err != ErrTokenNotFound {
		t.Fatalf("Load missing: got %v, want ErrTokenNotFound", err)
	}
	// Deleting a missing record is a no-op that still persists.
	if err := store.Delete(ctx, "nope"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func mustFileStore(t *testing.T, path string) *FileStore {
	t.Helper()
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}
