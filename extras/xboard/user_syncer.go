package xboard

import (
	"context"
	"fmt"
	"time"
)

type UsersClient interface {
	FetchUsers(ctx context.Context, etag string) (*UsersResponse, error)
}

type UserSyncResult struct {
	Updated     bool
	NotModified bool
	RevokedIDs  []string
}

// UserSyncer owns the explicit transitions from remote responses or a startup
// cache into the atomic last-known-good UserStore.
type UserSyncer struct {
	client    UsersClient
	store     *UserStore
	cachePath string
	maxStale  time.Duration
	now       func() time.Time
}

func NewUserSyncer(client UsersClient, store *UserStore, cachePath string, maxStale time.Duration) *UserSyncer {
	return &UserSyncer{
		client:    client,
		store:     store,
		cachePath: cachePath,
		maxStale:  maxStale,
		now:       time.Now,
	}
}

func (s *UserSyncer) LoadCache() error {
	if s == nil || s.store == nil {
		return fmt.Errorf("Xboard user store is required")
	}
	if s.cachePath == "" {
		return fmt.Errorf("Xboard user cache path is required")
	}
	snapshot, err := LoadUserCache(s.cachePath, s.now(), s.maxStale)
	if err != nil {
		return err
	}
	_, err = s.store.Replace(snapshot.Users, snapshot.ETag, snapshot.FetchedAt)
	return err
}

func (s *UserSyncer) Sync(ctx context.Context) (UserSyncResult, error) {
	if s == nil || s.client == nil {
		return UserSyncResult{}, fmt.Errorf("Xboard users client is required")
	}
	if s.store == nil {
		return UserSyncResult{}, fmt.Errorf("Xboard user store is required")
	}

	current := s.store.Snapshot()
	response, err := s.client.FetchUsers(ctx, current.ETag)
	if err != nil {
		return UserSyncResult{}, err
	}
	if response == nil {
		return UserSyncResult{}, fmt.Errorf("empty Xboard users response")
	}

	now := s.now()
	if response.NotModified {
		if current.FetchedAt.IsZero() {
			return UserSyncResult{}, fmt.Errorf("Xboard returned not modified without a local snapshot")
		}
		etag := response.ETag
		if etag == "" {
			etag = current.ETag
		}
		if _, err := s.store.Replace(current.Users, etag, now); err != nil {
			return UserSyncResult{}, err
		}
		if err := s.saveCurrent(); err != nil {
			return UserSyncResult{NotModified: true}, err
		}
		return UserSyncResult{NotModified: true}, nil
	}

	revoked, err := s.store.Replace(response.Users, response.ETag, now)
	if err != nil {
		return UserSyncResult{}, err
	}
	result := UserSyncResult{Updated: true, RevokedIDs: revoked}
	if err := s.saveCurrent(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *UserSyncer) saveCurrent() error {
	if s.cachePath == "" {
		return nil
	}
	return SaveUserCache(s.cachePath, s.store.Snapshot())
}
