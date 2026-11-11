package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultBasicCacheTTL is how long a verified Basic username and password is
// trusted before PBKDF2 runs again.
const DefaultBasicCacheTTL = 5 * time.Minute

// BasicOptions configures the HTTP Basic authenticator.
type BasicOptions struct {
	// Passwords also accepts users' real passwords. Without it only an API
	// token is accepted as the password.
	Passwords bool
	// CacheTTL is how long a verified password is remembered (as a keyed
	// hash, never the password); 0 means
	// DefaultBasicCacheTTL. Entries die early when the password changes.
	CacheTTL time.Duration
}

// Basic authenticates "Authorization: Basic". The password may be an API
// token (the username is then ignored, as tools such as git expect) or,
// with opts.Passwords, the user's password. Browsers cache Basic
// credentials, so principals are Ambient.
func (s *Service) Basic(opts BasicOptions) Authenticator {
	ttl := opts.CacheTTL
	if ttl <= 0 {
		ttl = DefaultBasicCacheTTL
	}
	cache := &basicCache{ttl: ttl, now: s.clock, entries: map[[32]byte]basicEntry{}}
	// Cache keys are HMACs under a random per-process key, so a leaked
	// key cannot be brute-forced back to a password outside this process.
	cacheKey := make([]byte, 32)
	_, _ = rand.Read(cacheKey) // never fails

	return AuthenticatorFunc(func(ctx context.Context, r Request) (*Principal, error) {
		creds, ok := authorization(r.Header, "Basic")
		if !ok {
			return nil, ErrNoCredentials
		}
		raw, err := base64.StdEncoding.DecodeString(creds)
		if err != nil {
			return nil, fmt.Errorf("%w: malformed Basic credentials", ErrUnauthenticated)
		}
		// The second field is a secret: an API token or, if allowed, a password.
		username, secret, ok := strings.Cut(string(raw), ":")
		if !ok {
			return nil, fmt.Errorf("%w: malformed Basic credentials", ErrUnauthenticated)
		}

		if strings.HasPrefix(secret, tokenMarker) {
			p, err := s.AuthenticateToken(ctx, secret)
			if errors.Is(err, ErrUnauthenticated) {
				return nil, fmt.Errorf("%w: invalid or expired token", ErrUnauthenticated)
			}
			if err != nil {
				return nil, err
			}
			p.Ambient = true
			return p, nil
		}
		if !opts.Passwords {
			return nil, fmt.Errorf("%w: Basic auth accepts API tokens only", ErrUnauthenticated)
		}

		mac := hmac.New(sha256.New, cacheKey)
		mac.Write([]byte(username + "\x00" + secret))
		key := [32]byte(mac.Sum(nil))
		if e, ok := cache.get(key); ok {
			u, err := s.store.GetUser(ctx, e.userID)
			// Valid only while the password (and name) are unchanged.
			if err == nil && u.PasswordHash == e.passwordHash && u.Username == username {
				return &Principal{User: u, Method: MethodBasic, Ambient: true}, nil
			}
			cache.drop(key)
		}
		u, err := s.checkPassword(ctx, username, secret, r.Peer)
		if errors.Is(err, ErrUnauthenticated) {
			return nil, fmt.Errorf("%w: invalid username or password", ErrUnauthenticated)
		}
		if err != nil {
			return nil, err
		}
		cache.put(key, basicEntry{userID: u.ID, passwordHash: u.PasswordHash})
		return &Principal{User: u, Method: MethodBasic, Ambient: true}, nil
	})
}

type basicEntry struct {
	userID       string
	passwordHash string
	expires      time.Time
}

// basicCache remembers verified credentials by an HMAC-SHA256 of
// "username\x00password" under a per-process random key; the password
// itself is never kept.
type basicCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[[32]byte]basicEntry
}

const basicCacheMax = 10_000

func (c *basicCache) get(key [32]byte) (basicEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok && !c.now().Before(e.expires) {
		delete(c.entries, key)
		return basicEntry{}, false
	}
	return e, ok
}

func (c *basicCache) put(key [32]byte, e basicEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= basicCacheMax {
		for k, v := range c.entries {
			if !now.Before(v.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= basicCacheMax {
			clear(c.entries) // all live: start over rather than grow unbounded
		}
	}
	e.expires = now.Add(c.ttl)
	c.entries[key] = e
}

func (c *basicCache) drop(key [32]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
