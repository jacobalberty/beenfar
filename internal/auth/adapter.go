package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jacobalberty/beenfar/internal/store"
)

// adapterTokenMarker starts every adapter token: "bfa_<prefix>_<secret>".
// It differs from the API token marker, so one kind never authenticates as
// the other.
const adapterTokenMarker = "bfa_"

var adapterName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidAdapterName reports whether name can name an adapter.
func ValidAdapterName(name string) bool { return adapterName.MatchString(name) }

// CreateAdapterToken issues a token that lets an external adapter act as
// the adapter called name. The returned secret is the only copy.
func (s *Service) CreateAdapterToken(ctx context.Context, name string) (store.AdapterToken, string, error) {
	if !ValidAdapterName(name) {
		return store.AdapterToken{}, "", invalid("adapter names are 1-32 characters: a lower-case letter, then letters, digits, '-' or '_'")
	}
	for attempt := 0; ; attempt++ {
		prefix := strings.ToLower(rand.Text()[:prefixLen])
		secret := adapterTokenMarker + prefix + "_" + rand.Text()
		t, err := s.store.CreateAdapterToken(ctx, store.AdapterToken{
			Adapter:    name,
			Prefix:     prefix,
			SecretHash: hashSecret(secret),
		})
		if errors.Is(err, store.ErrConflict) && attempt == 0 {
			continue
		}
		return t, secret, err
	}
}

// AuthenticateAdapterToken resolves an adapter token to the adapter it names.
func (s *Service) AuthenticateAdapterToken(ctx context.Context, token string) (string, error) {
	rest, ok := strings.CutPrefix(token, adapterTokenMarker)
	if !ok {
		return "", ErrUnauthenticated
	}
	prefix, _, ok := strings.Cut(rest, "_")
	if !ok || len(prefix) != prefixLen {
		return "", ErrUnauthenticated
	}
	t, err := s.store.GetAdapterTokenByPrefix(ctx, prefix)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(token)), []byte(t.SecretHash)) != 1 {
		return "", ErrUnauthenticated
	}
	if now := s.now(); now.Sub(t.LastUsedTime) >= touchInterval {
		if err := s.store.TouchAdapterToken(ctx, t.ID, now); err != nil {
			slog.WarnContext(ctx, "recording adapter token use", slog.Any("error", err))
		}
	}
	return t.Adapter, nil
}

// ListAdapterTokens lists every adapter token.
func (s *Service) ListAdapterTokens(ctx context.Context) ([]store.AdapterToken, error) {
	return s.store.ListAdapterTokens(ctx)
}

// DeleteAdapterToken revokes an adapter token.
func (s *Service) DeleteAdapterToken(ctx context.Context, id string) error {
	return s.store.DeleteAdapterToken(ctx, id)
}

// AdapterBearer extracts the adapter token from an Authorization header.
func AdapterBearer(h Header) (string, bool) { return authorization(h, "Bearer") }
