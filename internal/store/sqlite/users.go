package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite/gen/db"
)

// --- users ---

func toUser(u db.User) store.User {
	return store.User{
		ID:           u.ID,
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		Admin:        u.Admin,
		Revision:     u.Revision,
		CreateTime:   fromMilli(u.CreateTime),
		UpdateTime:   fromMilli(u.UpdateTime),
	}
}

func validateUsername(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("%w: username must be 1-64 bytes", store.ErrInvalid)
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, u store.User) (store.User, error) {
	if err := validateUsername(u.Username); err != nil {
		return store.User{}, err
	}
	if u.PasswordHash == "" {
		return store.User{}, fmt.Errorf("%w: password hash is required", store.ErrInvalid)
	}
	row, err := s.q.CreateUser(ctx, db.CreateUserParams{
		ID:           newID(),
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		Admin:        u.Admin,
		Now:          s.nowMilli(),
	})
	if err != nil {
		return store.User{}, mapErr(err)
	}
	return toUser(row), nil
}

func (s *Store) GetUser(ctx context.Context, id string) (store.User, error) {
	row, err := s.q.GetUser(ctx, id)
	if err != nil {
		return store.User{}, mapErr(err)
	}
	return toUser(row), nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (store.User, error) {
	row, err := s.q.GetUserByUsername(ctx, username)
	if err != nil {
		return store.User{}, mapErr(err)
	}
	return toUser(row), nil
}

func (s *Store) ListUsers(ctx context.Context) ([]store.User, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]store.User, 0, len(rows))
	for _, r := range rows {
		out = append(out, toUser(r))
	}
	return out, nil
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	n, err := s.q.CountUsers(ctx)
	return n, mapErr(err)
}

func (s *Store) CountAdmins(ctx context.Context) (int64, error) {
	n, err := s.q.CountAdmins(ctx)
	return n, mapErr(err)
}

func (s *Store) UpdateUser(ctx context.Context, u store.User) (store.User, error) {
	if err := validateUsername(u.Username); err != nil {
		return store.User{}, err
	}
	var out store.User
	err := s.inTx(ctx, func(txs *Store) error {
		row, err := txs.q.UpdateUser(ctx, db.UpdateUserParams{
			ID:               u.ID,
			Username:         u.Username,
			Admin:            u.Admin,
			Now:              txs.nowMilli(),
			ExpectedRevision: u.Revision,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return txs.revisionMiss(ctx, "user", u.ID, txs.userExists)
		}
		if err != nil {
			return mapErr(err)
		}
		out = toUser(row)
		return nil
	})
	return out, err
}

func (s *Store) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	if passwordHash == "" {
		return fmt.Errorf("%w: password hash is required", store.ErrInvalid)
	}
	n, err := s.q.SetUserPassword(ctx, db.SetUserPasswordParams{ID: id, PasswordHash: passwordHash, Now: s.nowMilli()})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id string, revision int64) error {
	return s.inTx(ctx, func(txs *Store) error {
		n, err := txs.q.DeleteUser(ctx, db.DeleteUserParams{ID: id, ExpectedRevision: revision})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return txs.revisionMiss(ctx, "user", id, txs.userExists)
		}
		return nil
	})
}

func (txs *Store) userExists(ctx context.Context, id string) error {
	_, err := txs.q.GetUser(ctx, id)
	return err
}

// --- API tokens ---

func toAPIToken(t db.ApiToken) store.APIToken {
	return store.APIToken{
		ID:           t.ID,
		UserID:       t.UserID,
		Name:         t.Name,
		Prefix:       t.Prefix,
		SecretHash:   t.SecretHash,
		ExpireTime:   fromOptMilli(t.ExpireTime),
		LastUsedTime: fromOptMilli(t.LastUsedTime),
		CreateTime:   fromMilli(t.CreateTime),
	}
}

func (s *Store) CreateAPIToken(ctx context.Context, t store.APIToken) (store.APIToken, error) {
	if t.Name == "" || len(t.Name) > 64 {
		return store.APIToken{}, fmt.Errorf("%w: name must be 1-64 bytes", store.ErrInvalid)
	}
	if t.UserID == "" || t.Prefix == "" || t.SecretHash == "" {
		return store.APIToken{}, fmt.Errorf("%w: user, prefix and secret hash are required", store.ErrInvalid)
	}
	row, err := s.q.CreateAPIToken(ctx, db.CreateAPITokenParams{
		ID:         newID(),
		UserID:     t.UserID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		SecretHash: t.SecretHash,
		ExpireTime: toOptMilli(t.ExpireTime),
		Now:        s.nowMilli(),
	})
	if err != nil {
		return store.APIToken{}, mapErr(err)
	}
	return toAPIToken(row), nil
}

func (s *Store) GetAPIToken(ctx context.Context, id string) (store.APIToken, error) {
	row, err := s.q.GetAPIToken(ctx, id)
	if err != nil {
		return store.APIToken{}, mapErr(err)
	}
	return toAPIToken(row), nil
}

func (s *Store) GetAPITokenByPrefix(ctx context.Context, prefix string) (store.APIToken, error) {
	row, err := s.q.GetAPITokenByPrefix(ctx, prefix)
	if err != nil {
		return store.APIToken{}, mapErr(err)
	}
	return toAPIToken(row), nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]store.APIToken, error) {
	rows, err := s.q.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]store.APIToken, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPIToken(r))
	}
	return out, nil
}

func (s *Store) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	return mapErr(s.q.TouchAPIToken(ctx, db.TouchAPITokenParams{ID: id, Now: at.UnixMilli()}))
}

func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	n, err := s.q.DeleteAPIToken(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- sessions ---

func toSession(s db.Session) store.Session {
	return store.Session{
		IDHash:     s.IDHash,
		UserID:     s.UserID,
		CreateTime: fromMilli(s.CreateTime),
		ExpireTime: fromMilli(s.ExpireTime),
	}
}

func (s *Store) CreateSession(ctx context.Context, sess store.Session) (store.Session, error) {
	if sess.IDHash == "" || sess.UserID == "" || sess.ExpireTime.IsZero() {
		return store.Session{}, fmt.Errorf("%w: id hash, user and expiry are required", store.ErrInvalid)
	}
	row, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		IDHash:     sess.IDHash,
		UserID:     sess.UserID,
		ExpireTime: sess.ExpireTime.UnixMilli(),
		Now:        s.nowMilli(),
	})
	if err != nil {
		return store.Session{}, mapErr(err)
	}
	return toSession(row), nil
}

func (s *Store) GetSession(ctx context.Context, idHash string) (store.Session, error) {
	row, err := s.q.GetSession(ctx, idHash)
	if err != nil {
		return store.Session{}, mapErr(err)
	}
	return toSession(row), nil
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	return mapErr(s.q.DeleteSession(ctx, idHash))
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	return mapErr(s.q.DeleteUserSessions(ctx, userID))
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.q.DeleteExpiredSessions(ctx, now.UnixMilli())
	return n, mapErr(err)
}
