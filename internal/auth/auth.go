// Package auth handles users, passwords, API tokens and browser sessions.
//
// Secrets are never stored: API tokens and session cookies are random strings
// whose SHA-256 is kept, and passwords are hashed with PBKDF2-HMAC-SHA256.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
)

var (
	// ErrUnauthenticated means no valid credentials were presented.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrPermissionDenied means the caller is authenticated but not allowed.
	ErrPermissionDenied = errors.New("permission denied")
)

const (
	// tokenMarker starts every API token, so leaked tokens are easy to spot
	// with secret scanners: "bf_<prefix>_<secret>".
	tokenMarker = "bf_"
	prefixLen   = 12

	// DefaultSessionTTL is how long a browser session lasts.
	DefaultSessionTTL = 7 * 24 * time.Hour

	// touchInterval limits how often a token's last-used time is written.
	touchInterval = time.Minute
)

// Method says how a principal authenticated.
type Method string

const (
	MethodToken   Method = "token"
	MethodSession Method = "session"
)

// Principal is an authenticated caller.
type Principal struct {
	User   store.User
	Method Method
	// TokenID is set for MethodToken; SessionHash for MethodSession.
	TokenID     string
	SessionHash string
}

// IsAdmin reports whether the principal may perform admin operations.
func (p *Principal) IsAdmin() bool { return p != nil && p.User.Admin }

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored by WithPrincipal, or nil.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// Service implements authentication and user management on a store.
type Service struct {
	store      store.Store
	iterations int
	sessionTTL time.Duration
	now        func() time.Time

	dummyOnce sync.Once
	dummy     string // hash compared against when a username is unknown
}

// Option configures a Service.
type Option func(*Service)

// WithIterations sets the PBKDF2 work factor for new hashes. Tests use a low
// value; existing hashes keep the factor they were created with.
func WithIterations(n int) Option { return func(s *Service) { s.iterations = n } }

// WithSessionTTL sets the browser session lifetime.
func WithSessionTTL(d time.Duration) Option { return func(s *Service) { s.sessionTTL = d } }

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// New returns a Service backed by st.
func New(st store.Store, opts ...Option) *Service {
	s := &Service{store: st, iterations: DefaultIterations, sessionTTL: DefaultSessionTTL, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CountUsers returns how many users exist.
func (s *Service) CountUsers(ctx context.Context) (int64, error) { return s.store.CountUsers(ctx) }

// SessionTTL returns the browser session lifetime.
func (s *Service) SessionTTL() time.Duration { return s.sessionTTL }

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", store.ErrInvalid, fmt.Sprintf(format, args...))
}

// --- users ---

// CreateUser creates a user with a new password.
func (s *Service) CreateUser(ctx context.Context, username, password string, admin bool) (store.User, error) {
	if err := validatePassword(password); err != nil {
		return store.User{}, invalid("%v", err)
	}
	hash, err := HashPassword(password, s.iterations)
	if err != nil {
		return store.User{}, err
	}
	return s.store.CreateUser(ctx, store.User{Username: username, PasswordHash: hash, Admin: admin})
}

// UpdateUser replaces a user's username and admin flag. It refuses to remove
// the last admin.
func (s *Service) UpdateUser(ctx context.Context, u store.User) (store.User, error) {
	var out store.User
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		cur, err := tx.GetUser(ctx, u.ID)
		if err != nil {
			return err
		}
		if cur.Admin && !u.Admin {
			if err := lastAdminCheck(ctx, tx); err != nil {
				return err
			}
		}
		out, err = tx.UpdateUser(ctx, u)
		return err
	})
	return out, err
}

// DeleteUser deletes a user. It refuses to remove the last admin.
func (s *Service) DeleteUser(ctx context.Context, id string, revision int64) error {
	return s.store.WithTx(ctx, func(tx store.Store) error {
		cur, err := tx.GetUser(ctx, id)
		if err != nil {
			return err
		}
		if cur.Admin {
			if err := lastAdminCheck(ctx, tx); err != nil {
				return err
			}
		}
		return tx.DeleteUser(ctx, id, revision)
	})
}

func lastAdminCheck(ctx context.Context, tx store.Store) error {
	n, err := tx.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return fmt.Errorf("%w: at least one admin must remain", store.ErrFailedPrecondition)
	}
	return nil
}

// SetPassword replaces a user's password and ends all of their sessions.
// When current is non-nil it must match the existing password.
func (s *Service) SetPassword(ctx context.Context, userID, password string, current *string) error {
	if err := validatePassword(password); err != nil {
		return invalid("%v", err)
	}
	hash, err := HashPassword(password, s.iterations)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx store.Store) error {
		if current != nil {
			u, err := tx.GetUser(ctx, userID)
			if err != nil {
				return err
			}
			ok, err := CheckPassword(u.PasswordHash, *current)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: current password is incorrect", ErrPermissionDenied)
			}
		}
		if err := tx.SetUserPassword(ctx, userID, hash); err != nil {
			return err
		}
		return tx.DeleteUserSessions(ctx, userID)
	})
}

// Bootstrap creates an admin named username when the controller has no
// users yet. It reports whether it created one.
func (s *Service) Bootstrap(ctx context.Context, username, password string) (bool, error) {
	created := false
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		n, err := tx.CountUsers(ctx)
		if err != nil || n > 0 {
			return err
		}
		if err := validatePassword(password); err != nil {
			return invalid("%v", err)
		}
		hash, err := HashPassword(password, s.iterations)
		if err != nil {
			return err
		}
		_, err = tx.CreateUser(ctx, store.User{Username: username, PasswordHash: hash, Admin: true})
		created = err == nil
		return err
	})
	return created, err
}

// --- sessions ---

// Login checks credentials and starts a session. It returns the cookie value,
// which is shown to the client once and stored only as a hash.
func (s *Service) Login(ctx context.Context, username, password string) (store.User, string, store.Session, error) {
	u, err := s.store.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// Spend the same time as a real check so usernames cannot be probed.
		_, _ = CheckPassword(s.dummyHash(), password)
		return store.User{}, "", store.Session{}, ErrUnauthenticated
	}
	if err != nil {
		return store.User{}, "", store.Session{}, err
	}
	ok, err := CheckPassword(u.PasswordHash, password)
	if err != nil {
		return store.User{}, "", store.Session{}, err
	}
	if !ok {
		return store.User{}, "", store.Session{}, ErrUnauthenticated
	}

	now := s.now()
	if _, err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		slog.WarnContext(ctx, "sweeping expired sessions", slog.Any("error", err))
	}
	cookie := rand.Text() + rand.Text()
	sess, err := s.store.CreateSession(ctx, store.Session{
		IDHash:     hashSecret(cookie),
		UserID:     u.ID,
		ExpireTime: now.Add(s.sessionTTL),
	})
	if err != nil {
		return store.User{}, "", store.Session{}, err
	}
	return u, cookie, sess, nil
}

// Logout ends the session identified by a cookie value.
func (s *Service) Logout(ctx context.Context, cookie string) error {
	return s.store.DeleteSession(ctx, hashSecret(cookie))
}

// AuthenticateSession resolves a session cookie value.
func (s *Service) AuthenticateSession(ctx context.Context, cookie string) (*Principal, error) {
	if cookie == "" {
		return nil, ErrUnauthenticated
	}
	h := hashSecret(cookie)
	sess, err := s.store.GetSession(ctx, h)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if !s.now().Before(sess.ExpireTime) {
		_ = s.store.DeleteSession(ctx, h)
		return nil, ErrUnauthenticated
	}
	u, err := s.store.GetUser(ctx, sess.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	return &Principal{User: u, Method: MethodSession, SessionHash: h}, nil
}

func (s *Service) dummyHash() string {
	s.dummyOnce.Do(func() {
		s.dummy, _ = HashPassword(rand.Text(), s.iterations)
	})
	return s.dummy
}

// --- API tokens ---

// CreateAPIToken issues a token for userID. The returned secret is the only
// copy of the token.
func (s *Service) CreateAPIToken(ctx context.Context, userID, name string, expire time.Time) (store.APIToken, string, error) {
	if !expire.IsZero() && !expire.After(s.now()) {
		return store.APIToken{}, "", invalid("expire_time must be in the future")
	}
	for attempt := 0; ; attempt++ {
		prefix := strings.ToLower(rand.Text()[:prefixLen])
		secret := tokenMarker + prefix + "_" + rand.Text()
		t, err := s.store.CreateAPIToken(ctx, store.APIToken{
			UserID:     userID,
			Name:       name,
			Prefix:     prefix,
			SecretHash: hashSecret(secret),
			ExpireTime: expire,
		})
		// A prefix collision (60 random bits) is astronomically unlikely, but
		// retrying once costs nothing.
		if errors.Is(err, store.ErrConflict) && attempt == 0 {
			continue
		}
		return t, secret, err
	}
}

// AuthenticateToken resolves a bearer token.
func (s *Service) AuthenticateToken(ctx context.Context, token string) (*Principal, error) {
	rest, ok := strings.CutPrefix(token, tokenMarker)
	if !ok {
		return nil, ErrUnauthenticated
	}
	prefix, _, ok := strings.Cut(rest, "_")
	if !ok || len(prefix) != prefixLen {
		return nil, ErrUnauthenticated
	}
	t, err := s.store.GetAPITokenByPrefix(ctx, prefix)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(token)), []byte(t.SecretHash)) != 1 {
		return nil, ErrUnauthenticated
	}
	now := s.now()
	if !t.ExpireTime.IsZero() && !now.Before(t.ExpireTime) {
		return nil, ErrUnauthenticated
	}
	u, err := s.store.GetUser(ctx, t.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if now.Sub(t.LastUsedTime) >= touchInterval {
		if err := s.store.TouchAPIToken(ctx, t.ID, now); err != nil {
			slog.WarnContext(ctx, "recording token use", slog.Any("error", err))
		}
	}
	return &Principal{User: u, Method: MethodToken, TokenID: t.ID}, nil
}

// ListAPITokens lists the principal's tokens, or all tokens for an admin.
func (s *Service) ListAPITokens(ctx context.Context, p *Principal) ([]store.APIToken, error) {
	if p.IsAdmin() {
		return s.store.ListAPITokens(ctx, "")
	}
	return s.store.ListAPITokens(ctx, p.User.ID)
}

// DeleteAPIToken revokes a token owned by the principal (any token for an
// admin). Tokens of other users look like they do not exist.
func (s *Service) DeleteAPIToken(ctx context.Context, p *Principal, id string) error {
	return s.store.WithTx(ctx, func(tx store.Store) error {
		t, err := tx.GetAPIToken(ctx, id)
		if err != nil {
			return err
		}
		if t.UserID != p.User.ID && !p.IsAdmin() {
			return store.ErrNotFound
		}
		return tx.DeleteAPIToken(ctx, id)
	})
}
