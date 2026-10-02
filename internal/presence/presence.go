// Package presence keeps, per access key, what tam-server last saw of the
// client holding it: when it was seen, when it last wrote, what its
// heartbeat reported as queued, and which program it is. It lives in
// memory only and is exact; the store keeps the coarse, throttled
// last_seen and last_update times that survive a restart.
package presence

import (
	"sync"
	"time"
)

// Record is what is known about one key's client.
type Record struct {
	Seen       time.Time // the last keyed request
	Updated    time.Time // the last accepted write; zero when none
	Pending    int       // queued saves, from the last heartbeat
	HasPending bool      // whether a heartbeat ever reported Pending
	Client     string    // the program, as its last request named it
}

// Registry is the thread-safe table of records.
type Registry struct {
	now func() time.Time

	mu    sync.Mutex
	byKey map[string]Record
}

// New returns an empty registry that reads the time from now; nil means
// the wall clock.
func New(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{now: now, byKey: map[string]Record{}}
}

// Seen records a keyed request from client. An empty client keeps the
// name the key had.
func (r *Registry) Seen(key, client string) {
	r.mu.Lock()
	r.see(key, client)
	r.mu.Unlock()
}

// Heartbeat records a keyed request that reports pending queued saves.
func (r *Registry) Heartbeat(key, client string, pending int) {
	r.mu.Lock()
	rec := r.see(key, client)
	rec.Pending, rec.HasPending = pending, true
	r.byKey[key] = rec
	r.mu.Unlock()
}

// Updated records an accepted write, which also counts as seeing the key.
func (r *Registry) Updated(key string) {
	r.mu.Lock()
	rec := r.see(key, "")
	rec.Updated = rec.Seen
	r.byKey[key] = rec
	r.mu.Unlock()
}

// Forget drops the key's record, for when the key is deleted.
func (r *Registry) Forget(key string) {
	r.mu.Lock()
	delete(r.byKey, key)
	r.mu.Unlock()
}

// Snapshot returns a copy of every record by key.
func (r *Registry) Snapshot() map[string]Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Record, len(r.byKey))
	for k, rec := range r.byKey {
		out[k] = rec
	}
	return out
}

// see stamps the key as seen now, stores the record and returns it. The
// caller holds the lock.
func (r *Registry) see(key, client string) Record {
	rec := r.byKey[key]
	rec.Seen = r.now()
	if client != "" {
		rec.Client = client
	}
	r.byKey[key] = rec
	return rec
}
