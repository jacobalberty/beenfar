package otelstore

import (
	"context"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
)

func (s *Store) CreateNetwork(ctx context.Context, n store.Network) (store.Network, error) {
	return run(ctx, s, "CreateNetwork", func(ctx context.Context) (store.Network, error) { return s.next.CreateNetwork(ctx, n) })
}

func (s *Store) GetNetwork(ctx context.Context, id string) (store.Network, error) {
	return run(ctx, s, "GetNetwork", func(ctx context.Context) (store.Network, error) { return s.next.GetNetwork(ctx, id) })
}

func (s *Store) ListNetworks(ctx context.Context) ([]store.Network, error) {
	return run(ctx, s, "ListNetworks", func(ctx context.Context) ([]store.Network, error) { return s.next.ListNetworks(ctx) })
}

func (s *Store) UpdateNetwork(ctx context.Context, n store.Network) (store.Network, error) {
	return run(ctx, s, "UpdateNetwork", func(ctx context.Context) (store.Network, error) { return s.next.UpdateNetwork(ctx, n) })
}

func (s *Store) DeleteNetwork(ctx context.Context, id string, revision int64) error {
	return run0(ctx, s, "DeleteNetwork", func(ctx context.Context) error { return s.next.DeleteNetwork(ctx, id, revision) })
}

func (s *Store) CreateUser(ctx context.Context, u store.User) (store.User, error) {
	return run(ctx, s, "CreateUser", func(ctx context.Context) (store.User, error) { return s.next.CreateUser(ctx, u) })
}

func (s *Store) GetUser(ctx context.Context, id string) (store.User, error) {
	return run(ctx, s, "GetUser", func(ctx context.Context) (store.User, error) { return s.next.GetUser(ctx, id) })
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (store.User, error) {
	return run(ctx, s, "GetUserByUsername", func(ctx context.Context) (store.User, error) { return s.next.GetUserByUsername(ctx, username) })
}

func (s *Store) ListUsers(ctx context.Context) ([]store.User, error) {
	return run(ctx, s, "ListUsers", func(ctx context.Context) ([]store.User, error) { return s.next.ListUsers(ctx) })
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	return run(ctx, s, "CountUsers", func(ctx context.Context) (int64, error) { return s.next.CountUsers(ctx) })
}

func (s *Store) CountAdmins(ctx context.Context) (int64, error) {
	return run(ctx, s, "CountAdmins", func(ctx context.Context) (int64, error) { return s.next.CountAdmins(ctx) })
}

func (s *Store) UpdateUser(ctx context.Context, u store.User) (store.User, error) {
	return run(ctx, s, "UpdateUser", func(ctx context.Context) (store.User, error) { return s.next.UpdateUser(ctx, u) })
}

func (s *Store) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	return run0(ctx, s, "SetUserPassword", func(ctx context.Context) error { return s.next.SetUserPassword(ctx, id, passwordHash) })
}

func (s *Store) DeleteUser(ctx context.Context, id string, revision int64) error {
	return run0(ctx, s, "DeleteUser", func(ctx context.Context) error { return s.next.DeleteUser(ctx, id, revision) })
}

func (s *Store) CreateAPIToken(ctx context.Context, t store.APIToken) (store.APIToken, error) {
	return run(ctx, s, "CreateAPIToken", func(ctx context.Context) (store.APIToken, error) { return s.next.CreateAPIToken(ctx, t) })
}

func (s *Store) GetAPIToken(ctx context.Context, id string) (store.APIToken, error) {
	return run(ctx, s, "GetAPIToken", func(ctx context.Context) (store.APIToken, error) { return s.next.GetAPIToken(ctx, id) })
}

func (s *Store) GetAPITokenByPrefix(ctx context.Context, prefix string) (store.APIToken, error) {
	return run(ctx, s, "GetAPITokenByPrefix", func(ctx context.Context) (store.APIToken, error) { return s.next.GetAPITokenByPrefix(ctx, prefix) })
}

func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]store.APIToken, error) {
	return run(ctx, s, "ListAPITokens", func(ctx context.Context) ([]store.APIToken, error) { return s.next.ListAPITokens(ctx, userID) })
}

func (s *Store) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	return run0(ctx, s, "TouchAPIToken", func(ctx context.Context) error { return s.next.TouchAPIToken(ctx, id, at) })
}

func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	return run0(ctx, s, "DeleteAPIToken", func(ctx context.Context) error { return s.next.DeleteAPIToken(ctx, id) })
}

func (s *Store) CreateSession(ctx context.Context, sess store.Session) (store.Session, error) {
	return run(ctx, s, "CreateSession", func(ctx context.Context) (store.Session, error) { return s.next.CreateSession(ctx, sess) })
}

func (s *Store) GetSession(ctx context.Context, idHash string) (store.Session, error) {
	return run(ctx, s, "GetSession", func(ctx context.Context) (store.Session, error) { return s.next.GetSession(ctx, idHash) })
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	return run0(ctx, s, "DeleteSession", func(ctx context.Context) error { return s.next.DeleteSession(ctx, idHash) })
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	return run0(ctx, s, "DeleteUserSessions", func(ctx context.Context) error { return s.next.DeleteUserSessions(ctx, userID) })
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	return run(ctx, s, "DeleteExpiredSessions", func(ctx context.Context) (int64, error) { return s.next.DeleteExpiredSessions(ctx, now) })
}

func (s *Store) GetUserByIdentity(ctx context.Context, provider, subject string) (store.User, error) {
	return run(ctx, s, "GetUserByIdentity", func(ctx context.Context) (store.User, error) {
		return s.next.GetUserByIdentity(ctx, provider, subject)
	})
}

func (s *Store) CreateExternalUser(ctx context.Context, u store.User, provider, subject string) (store.User, error) {
	return run(ctx, s, "CreateExternalUser", func(ctx context.Context) (store.User, error) {
		return s.next.CreateExternalUser(ctx, u, provider, subject)
	})
}

func (s *Store) CreateAdapterToken(ctx context.Context, t store.AdapterToken) (store.AdapterToken, error) {
	return run(ctx, s, "CreateAdapterToken", func(ctx context.Context) (store.AdapterToken, error) { return s.next.CreateAdapterToken(ctx, t) })
}

func (s *Store) GetAdapterTokenByPrefix(ctx context.Context, prefix string) (store.AdapterToken, error) {
	return run(ctx, s, "GetAdapterTokenByPrefix", func(ctx context.Context) (store.AdapterToken, error) {
		return s.next.GetAdapterTokenByPrefix(ctx, prefix)
	})
}

func (s *Store) ListAdapterTokens(ctx context.Context) ([]store.AdapterToken, error) {
	return run(ctx, s, "ListAdapterTokens", func(ctx context.Context) ([]store.AdapterToken, error) { return s.next.ListAdapterTokens(ctx) })
}

func (s *Store) TouchAdapterToken(ctx context.Context, id string, at time.Time) error {
	return run0(ctx, s, "TouchAdapterToken", func(ctx context.Context) error { return s.next.TouchAdapterToken(ctx, id, at) })
}

func (s *Store) DeleteAdapterToken(ctx context.Context, id string) error {
	return run0(ctx, s, "DeleteAdapterToken", func(ctx context.Context) error { return s.next.DeleteAdapterToken(ctx, id) })
}

func (s *Store) RecordFieldWriters(ctx context.Context, resource store.FieldResource, id string, fields []string, w store.FieldWriter) error {
	return run0(ctx, s, "RecordFieldWriters", func(ctx context.Context) error {
		return s.next.RecordFieldWriters(ctx, resource, id, fields, w)
	})
}

func (s *Store) GetFieldWriters(ctx context.Context, resource store.FieldResource, id string) ([]store.FieldWriter, error) {
	return run(ctx, s, "GetFieldWriters", func(ctx context.Context) ([]store.FieldWriter, error) {
		return s.next.GetFieldWriters(ctx, resource, id)
	})
}

func (s *Store) ListFieldWriters(ctx context.Context, resource store.FieldResource) (map[string][]store.FieldWriter, error) {
	return run(ctx, s, "ListFieldWriters", func(ctx context.Context) (map[string][]store.FieldWriter, error) {
		return s.next.ListFieldWriters(ctx, resource)
	})
}
