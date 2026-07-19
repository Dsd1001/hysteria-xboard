package xboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	userCacheVersion = 1
	maxUserCacheSize = 32 << 20
)

var ErrUserCacheStale = errors.New("Xboard user cache is stale")

type userCacheFile struct {
	Version   int       `json:"version"`
	FetchedAt time.Time `json:"fetched_at"`
	ETag      string    `json:"etag,omitempty"`
	Users     []User    `json:"users"`
}

// SaveUserCache validates and atomically persists a last-known-good user snapshot.
// The resulting file is private because Xboard UUIDs are authentication secrets.
func SaveUserCache(path string, snapshot UserSnapshot) error {
	if path == "" {
		return fmt.Errorf("Xboard user cache path is required")
	}
	if snapshot.FetchedAt.IsZero() {
		return fmt.Errorf("Xboard user cache fetch time is required")
	}
	if err := validateUsers(snapshot.Users); err != nil {
		return err
	}

	cache := userCacheFile{
		Version:   userCacheVersion,
		FetchedAt: snapshot.FetchedAt.UTC(),
		ETag:      snapshot.ETag,
		Users:     append([]User(nil), snapshot.Users...),
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("encode Xboard user cache: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Xboard user cache directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".xboard-users-*")
	if err != nil {
		return fmt.Errorf("create temporary Xboard user cache: %w", err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary Xboard user cache: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary Xboard user cache: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary Xboard user cache: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary Xboard user cache: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace Xboard user cache: %w", err)
	}
	removeTemp = false

	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync Xboard user cache directory: %w", err)
	}
	return nil
}

// LoadUserCache loads and validates a persisted user snapshot. maxAge <= 0
// disables staleness enforcement.
func LoadUserCache(path string, now time.Time, maxAge time.Duration) (UserSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return UserSnapshot{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUserCacheSize+1))
	if err != nil {
		return UserSnapshot{}, fmt.Errorf("read Xboard user cache: %w", err)
	}
	if len(data) > maxUserCacheSize {
		return UserSnapshot{}, fmt.Errorf("Xboard user cache exceeds maximum size")
	}

	var cache userCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return UserSnapshot{}, fmt.Errorf("decode Xboard user cache: %w", err)
	}
	if cache.Version != userCacheVersion {
		return UserSnapshot{}, fmt.Errorf("unsupported Xboard user cache version: %d", cache.Version)
	}
	if cache.FetchedAt.IsZero() {
		return UserSnapshot{}, fmt.Errorf("Xboard user cache fetch time is required")
	}
	if err := validateUsers(cache.Users); err != nil {
		return UserSnapshot{}, err
	}
	if maxAge > 0 && now.After(cache.FetchedAt.Add(maxAge)) {
		return UserSnapshot{}, ErrUserCacheStale
	}

	return UserSnapshot{
		Users:     append([]User(nil), cache.Users...),
		ETag:      cache.ETag,
		FetchedAt: cache.FetchedAt,
	}, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
