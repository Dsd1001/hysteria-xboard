package xboard

import (
	"net"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
)

var _ server.Authenticator = (*Authenticator)(nil)

// Authenticator validates credentials exclusively against the current local
// Xboard snapshot. It never performs network I/O on the authentication path.
type Authenticator struct {
	store    *UserStore
	maxStale time.Duration
	now      func() time.Time
}

func NewAuthenticator(store *UserStore, maxStale time.Duration) *Authenticator {
	return &Authenticator{
		store:    store,
		maxStale: maxStale,
		now:      time.Now,
	}
}

func (a *Authenticator) Authenticate(_ net.Addr, auth string, _ uint64) (bool, string) {
	if a == nil || a.store == nil {
		return false, ""
	}
	id, fetchedAt, ok := a.store.LookupAuth(auth)
	if !ok {
		return false, ""
	}
	if fetchedAt.IsZero() {
		return false, ""
	}
	if a.maxStale > 0 && a.now().After(fetchedAt.Add(a.maxStale)) {
		return false, ""
	}
	return true, id
}
