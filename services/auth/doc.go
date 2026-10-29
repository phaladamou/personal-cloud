// Package auth identifies callers to the Personal Cloud control plane.
//
// An Identity binds a user (packages/types.User) to one of their devices.
// A device obtains its bearer token with POST /v1/auth/login and sends
// it back as `Authorization: Bearer <token>` on every /v1/* call.
//
// Token format (versioned, opaque, HMAC-signed):
//
//		pc1.<base64url(tokenID)>.<base64url(HMAC-SHA256(secret, tokenID))>
//
//	  - `pc1` is the format version.
//	  - tokenID is 16 random bytes; its base64url (no padding) encoding
//	    is also the lookup key of the token record in the Store.
//	  - The signature binds the id to the server secret, so forged ids
//	    fail before any store lookup (compared in constant time).
//
// The token is opaque: it carries no claims. Expiry and revocation are
// server-side (the record in the Store), so a stolen token dies with
// Revoke and an expired token is rejected even with a valid signature.
//
// Secrets never appear in errors or logs: failures collapse to
// ErrInvalidToken / ErrExpiredToken with no token material attached.
package auth
