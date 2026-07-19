package xboard

import (
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// UserSnapshot is a defensive copy of the last valid Xboard user response.
type UserSnapshot struct {
	Users     []User
	ETag      string
	FetchedAt time.Time
}

type userIndex struct {
	users     []User
	byAuth    map[string]string
	activeIDs map[string]struct{}
	etag      string
	fetchedAt time.Time
}

// UserStore provides lock-free authentication reads and atomic snapshot updates.
type UserStore struct {
	replaceMu sync.Mutex
	current   atomic.Pointer[userIndex]
}

func NewUserStore() *UserStore {
	store := &UserStore{}
	store.current.Store(emptyUserIndex())
	return store
}

func emptyUserIndex() *userIndex {
	return &userIndex{
		byAuth:    make(map[string]string),
		activeIDs: make(map[string]struct{}),
	}
}

// Replace validates and atomically installs a complete user snapshot. It returns
// the stable Xboard user IDs that were present previously but are no longer active.
// A validation error leaves the last-known-good snapshot untouched.
func (s *UserStore) Replace(users []User, etag string, fetchedAt time.Time) ([]string, error) {
	if err := validateUsers(users); err != nil {
		return nil, err
	}

	copiedUsers := append([]User(nil), users...)
	next := &userIndex{
		users:     copiedUsers,
		byAuth:    make(map[string]string, len(copiedUsers)),
		activeIDs: make(map[string]struct{}, len(copiedUsers)),
		etag:      etag,
		fetchedAt: fetchedAt,
	}
	for _, user := range copiedUsers {
		id := strconv.FormatInt(user.ID, 10)
		next.byAuth[user.UUID] = id
		next.activeIDs[id] = struct{}{}
	}

	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()

	previous := s.load()
	revoked := make([]string, 0)
	for id := range previous.activeIDs {
		if _, ok := next.activeIDs[id]; !ok {
			revoked = append(revoked, id)
		}
	}
	sort.Strings(revoked)
	s.current.Store(next)
	return revoked, nil
}

func (s *UserStore) load() *userIndex {
	if s == nil {
		return emptyUserIndex()
	}
	current := s.current.Load()
	if current == nil {
		return emptyUserIndex()
	}
	return current
}

// Lookup returns the stable decimal Xboard user ID for an authentication token.
func (s *UserStore) Lookup(auth string) (string, bool) {
	id, ok := s.load().byAuth[auth]
	return id, ok
}

// LookupAuth returns the credential result and freshness timestamp from one
// immutable snapshot, avoiding a lookup/freshness TOCTOU during replacement.
func (s *UserStore) LookupAuth(auth string) (string, time.Time, bool) {
	current := s.load()
	id, ok := current.byAuth[auth]
	return id, current.fetchedAt, ok
}

// IsActiveID reports whether a stable Xboard user ID is in the current snapshot.
func (s *UserStore) IsActiveID(id string) bool {
	_, ok := s.load().activeIDs[id]
	return ok
}

// Snapshot returns a defensive copy of the current last-known-good snapshot.
func (s *UserStore) Snapshot() UserSnapshot {
	current := s.load()
	return UserSnapshot{
		Users:     append([]User(nil), current.users...),
		ETag:      current.etag,
		FetchedAt: current.fetchedAt,
	}
}
