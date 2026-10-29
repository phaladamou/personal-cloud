package types

import (
	"fmt"
	"strings"
	"time"
)

// User is the identity that owns workspaces, sessions and resources.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// NewUser builds a validated User. id and email are required; email must
// contain "@" and a domain dot.
func NewUser(id, email, name string) (User, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return User{}, err
	}
	email = strings.TrimSpace(email)
	if !strings.Contains(email, "@") || !strings.Contains(strings.SplitN(email, "@", 2)[1], ".") {
		return User{}, fmt.Errorf("types: invalid email %q", email)
	}
	return User{ID: id, Email: email, Name: strings.TrimSpace(name), CreatedAt: nowUTC()}, nil
}

// String returns a short human-readable representation of the user.
func (u User) String() string {
	if u.Name != "" {
		return fmt.Sprintf("%s <%s>", u.Name, u.Email)
	}
	return u.Email
}
