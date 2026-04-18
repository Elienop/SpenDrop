package api

import (
	"sync"
	"time"
)

const summaryCacheTTL = 15 * time.Second

// cacheEntry holds a single pre-marshalled JSON payload and its provenance
// timestamps. payload is stored as []byte rather than a typed struct so the
// hot path (cache hit) is a single mutex-protected map lookup + memcopy, with
// no further JSON encode/decode.
//
// YAGNI decision D: a per-entry byte slice is slightly over-allocated vs.
// sharing a backing buffer, but at household-scale request rates (one scrape
// every 30-60 s per token) the allocator overhead is immeasurable. Revisit
// only if profiling shows this in a hot path.
type cacheEntry struct {
	payload   []byte
	asOf      time.Time
	expiresAt time.Time
}

// summaryCache is a per-token LRU-less in-memory cache for the
// /api/homepage/summary payload. The key is the SHA-256 token hash (hex
// string) so each physical token gets its own TTL slot — a second token for
// the same user does not share a stale slot from the first token (spec §5.3).
//
// YAGNI decision F: no eviction / bounded size. A household deployment has
// O(10) live tokens; the map will never grow meaningfully. If this is ever
// ported to a multi-tenant SaaS context an LRU cap would be warranted.
//
// sync.RWMutex is used instead of sync.Map because:
//   - The read path (get) must compose a staleness check atomically with the
//     lookup — sync.Map's Load returns the value but not the expiresAt, so a
//     second atomic step would be needed anyway, re-introducing a race window.
//   - At O(10) tokens the cache is effectively always warm; RLock contention
//     is negligible.
type summaryCache struct {
	mu    sync.RWMutex
	m     map[string]cacheEntry
	clock Clock
}

// newSummaryCache returns a ready-to-use cache backed by the given clock.
// Tests pass a fixedClock{} so TTL expiry is fully deterministic.
func newSummaryCache(clock Clock) *summaryCache {
	return &summaryCache{
		m:     make(map[string]cacheEntry),
		clock: clock,
	}
}

// get returns the cached entry for tokenHash if it exists and has not yet
// expired. The second return is false on both a miss and a stale entry so the
// caller does not need to distinguish the two cases.
func (c *summaryCache) get(tokenHash string) (cacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.m[tokenHash]
	c.mu.RUnlock()
	if !ok {
		return cacheEntry{}, false
	}
	if !c.clock.Now().Before(entry.expiresAt) {
		return cacheEntry{}, false
	}
	return entry, true
}

// put stores payload under tokenHash with an expiry of asOf + summaryCacheTTL.
// asOf should be the same time.Time the handler used for the DB aggregation
// window so that the as_of field in the JSON exactly matches the instant at
// which the data was collected, regardless of when the response is served.
func (c *summaryCache) put(tokenHash string, payload []byte, asOf time.Time) {
	c.mu.Lock()
	c.m[tokenHash] = cacheEntry{
		payload:   payload,
		asOf:      asOf,
		expiresAt: asOf.Add(summaryCacheTTL),
	}
	c.mu.Unlock()
}
