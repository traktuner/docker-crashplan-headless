package engine

import (
	"runtime"
	"sync"
	"time"
	"weak"
)

// clientCaches holds short-lived read caches that belong to one Client. They
// live in a side table keyed by a weak pointer so the Client struct stays
// untouched; the entry is dropped when its Client is collected.
type clientCaches struct {
	targetsMu  sync.Mutex
	targets    []Device
	targetsAt  time.Time
	targetsRev uint64

	historyMu  sync.Mutex
	history    []HistoryEntry
	historyAt  time.Time
	historyRev uint64
}

var cacheTable sync.Map // weak.Pointer[Client] -> *clientCaches

func (c *Client) caches() *clientCaches {
	key := weak.Make(c)
	if v, ok := cacheTable.Load(key); ok {
		return v.(*clientCaches)
	}
	v, loaded := cacheTable.LoadOrStore(key, &clientCaches{})
	if !loaded {
		runtime.AddCleanup(c, func(k weak.Pointer[Client]) { cacheTable.Delete(k) }, key)
	}
	return v.(*clientCaches)
}

// targetsFresh bounds how long the computer list used by progress polling is
// reused. GetComputers is the heavy part of a progress reading; the stats per
// target are re-read on every poll. A fresh device list (GET /api/v1/devices)
// or InvalidateTargets replaces it earlier.
const targetsFresh = 60 * time.Second

// InvalidateTargets drops the cached computer list, for example when the
// engine reports a computer connected, disconnected or changed.
func (c *Client) InvalidateTargets() {
	cache := c.caches()
	cache.targetsMu.Lock()
	cache.targets, cache.targetsAt = nil, time.Time{}
	cache.targetsMu.Unlock()
}

func (c *Client) storeTargets(revision uint64, devices []Device) {
	cache := c.caches()
	cache.targetsMu.Lock()
	cache.targets, cache.targetsAt, cache.targetsRev = devices, time.Now(), revision
	cache.targetsMu.Unlock()
}

func (c *Client) cachedTargets(revision uint64) ([]Device, bool) {
	cache := c.caches()
	cache.targetsMu.Lock()
	defer cache.targetsMu.Unlock()
	if cache.targets == nil || cache.targetsRev != revision || time.Since(cache.targetsAt) >= targetsFresh {
		return nil, false
	}
	return cache.targets, true
}
