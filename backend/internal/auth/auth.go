// Package auth is deliberately small: username + password signup (the
// "minimal details" brief), bcrypt at rest, opaque random session tokens
// stored server-side, 30-day expiry. Every user gets an isolated paper
// account with a starting equity of their choosing.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

const SessionTTL = 30 * 24 * time.Hour

// Bounds for "change equity to any number" — any number within reason.
var (
	MinStartingCash = decimal.NewFromInt(1_000)
	MaxStartingCash = decimal.NewFromInt(1_000_000_000) // ₹100 crore
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)

// CredentialError is safe to show to the user (bad input, wrong password).
type CredentialError struct{ Reason string }

func (e CredentialError) Error() string { return e.Reason }

func bad(format string, args ...any) CredentialError {
	return CredentialError{Reason: fmt.Sprintf(format, args...)}
}

type Service struct {
	store *store.Store
}

func New(st *store.Store) *Service { return &Service{store: st} }

// Signup creates the account and logs it straight in.
func (s *Service) Signup(ctx context.Context, username, password string, startingCash decimal.Decimal) (store.Account, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	switch {
	case !usernameRe.MatchString(username):
		return store.Account{}, "", bad("username must be 3–32 characters: letters, digits, dot, dash, underscore")
	case len(password) < 6:
		return store.Account{}, "", bad("password must be at least 6 characters")
	case len(password) > 72:
		return store.Account{}, "", bad("password must be at most 72 characters")
	}
	if startingCash.IsZero() {
		startingCash = decimal.NewFromInt(1_000_000) // the default ₹10 lakh
	}
	if startingCash.LessThan(MinStartingCash) || startingCash.GreaterThan(MaxStartingCash) {
		return store.Account{}, "", bad("starting equity must be between ₹%s and ₹%s",
			MinStartingCash.String(), MaxStartingCash.String())
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return store.Account{}, "", err
	}

	acct, err := s.store.CreateUser(ctx, username, string(hash), startingCash)
	if errors.Is(err, store.ErrNameTaken) {
		return store.Account{}, "", bad("that username is taken")
	}
	if err != nil {
		return store.Account{}, "", err
	}

	token, err := s.startSession(ctx, acct.ID)
	return acct, token, err
}

// Login verifies credentials and mints a session.
func (s *Service) Login(ctx context.Context, username, password string) (store.Account, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	acct, err := s.store.GetAccountByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, "", bad("wrong username or password")
	}
	if err != nil {
		return store.Account{}, "", err
	}
	// Legacy pre-auth accounts (e.g. "default") have no password and cannot
	// be logged into — they simply age out.
	if acct.PasswordHash == nil ||
		bcrypt.CompareHashAndPassword([]byte(*acct.PasswordHash), []byte(password)) != nil {
		return store.Account{}, "", bad("wrong username or password")
	}

	token, err := s.startSession(ctx, acct.ID)
	return acct, token, err
}

// Authenticate resolves a bearer token to its account.
func (s *Service) Authenticate(ctx context.Context, token string) (store.Account, error) {
	if token == "" {
		return store.Account{}, bad("not signed in")
	}
	acct, err := s.store.SessionAccount(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, bad("session expired — sign in again")
	}
	return acct, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, token)
}

func (s *Service) startSession(ctx context.Context, accountID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := "pt_" + hex.EncodeToString(raw)
	if err := s.store.CreateSession(ctx, token, accountID, SessionTTL); err != nil {
		return "", err
	}
	return token, nil
}
