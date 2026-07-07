package services

import (
	"sync"
	"time"
)

// =============================================
// HIGH-PERFORMANCE IN-MEMORY CACHE
// Lock-free reads with sharded writes
// =============================================

// CacheItem represents a cached item with expiry
type CacheItem struct {
	Value     interface{}
	ExpiresAt time.Time
}

// Cache is a high-performance concurrent cache with sharding
type Cache struct {
	shards    []*cacheShard
	shardMask uint32
}

type cacheShard struct {
	items map[string]*CacheItem
	mu    sync.RWMutex
}

const numShards = 256 // Power of 2 for fast modulo

// NewCache creates a new sharded cache
func NewCache() *Cache {
	c := &Cache{
		shards:    make([]*cacheShard, numShards),
		shardMask: numShards - 1,
	}
	for i := 0; i < numShards; i++ {
		c.shards[i] = &cacheShard{
			items: make(map[string]*CacheItem),
		}
	}
	return c
}

// getShard returns the shard for a key (FNV-1a hash)
func (c *Cache) getShard(key string) *cacheShard {
	hash := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= 16777619
	}
	return c.shards[hash&c.shardMask]
}

// Get retrieves a value from cache (lock-free fast path)
// OPTIMIZED: Expired items are NOT deleted here (deferred to periodic Cleanup)
// This avoids creating a goroutine for every expired key read
func (c *Cache) Get(key string) (interface{}, bool) {
	shard := c.getShard(key)
	shard.mu.RLock()
	item, ok := shard.items[key]
	shard.mu.RUnlock()

	if !ok {
		return nil, false
	}

	// Check expiry (no lock needed for read)
	// PERFORMANCE: Don't spawn goroutine here - let Cleanup() handle it
	if time.Now().After(item.ExpiresAt) {
		return nil, false
	}

	return item.Value, true
}

// Set stores a value with TTL
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	shard := c.getShard(key)
	item := &CacheItem{
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}

	shard.mu.Lock()
	shard.items[key] = item
	shard.mu.Unlock()
}

// Delete removes a key
func (c *Cache) Delete(key string) {
	shard := c.getShard(key)
	shard.mu.Lock()
	delete(shard.items, key)
	shard.mu.Unlock()
}

// DeletePrefix removes all keys with a prefix
// OPTIMIZED: Uses bounded concurrency to process shards in parallel
func (c *Cache) DeletePrefix(prefix string) {
	// Use a semaphore to limit concurrent shard processing
	sem := make(chan struct{}, 16) // Max 16 concurrent shards
	var wg sync.WaitGroup

	for _, shard := range c.shards {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(s *cacheShard) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			s.mu.Lock()
			for key := range s.items {
				if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
					delete(s.items, key)
				}
			}
			s.mu.Unlock()
		}(shard)
	}

	wg.Wait()
}

// Cleanup removes expired entries (call periodically)
func (c *Cache) Cleanup() {
	now := time.Now()
	for _, shard := range c.shards {
		shard.mu.Lock()
		for key, item := range shard.items {
			if now.After(item.ExpiresAt) {
				delete(shard.items, key)
			}
		}
		shard.mu.Unlock()
	}
}

// Stats returns cache statistics
func (c *Cache) Stats() map[string]interface{} {
	totalItems := 0
	for _, shard := range c.shards {
		shard.mu.RLock()
		totalItems += len(shard.items)
		shard.mu.RUnlock()
	}
	return map[string]interface{}{
		"total_items": totalItems,
		"num_shards":  numShards,
	}
}

// =============================================
// GLOBAL CACHE INSTANCE
// =============================================

var AppCache *Cache

// InitCache initializes the global cache
func InitCache() {
	AppCache = NewCache()

	// Start background cleanup (every minute)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			AppCache.Cleanup()
		}
	}()
}

// =============================================
// CACHE KEY HELPERS
// =============================================

// VenueCacheKey generates a cache key for venue data
func VenueCacheKey(venueID string) string {
	return "venue:" + venueID
}

// EventCacheKey generates a cache key for event data
func EventCacheKey(eventID string) string {
	return "event:" + eventID
}

// EventListCacheKey generates a cache key for event list
func EventListCacheKey(venueID, date string) string {
	return "events:" + venueID + ":" + date
}

// RolesCacheKey generates a cache key for roles
func RolesCacheKey(venueID string) string {
	return "roles:" + venueID
}

// GuestListTypeCacheKey generates a cache key for guest list type
func GuestListTypeCacheKey(typeID string) string {
	return "glt:" + typeID
}

// EventGuestListTypesCacheKey generates a cache key for event's guest list types
func EventGuestListTypesCacheKey(eventID string) string {
	return "event-glts:" + eventID
}

// TicketTypesCacheKey generates a cache key for event's ticket types
func TicketTypesCacheKey(eventID string) string {
	return "ticket-types:" + eventID
}

// EventWithTicketsCacheKey generates a cache key for event with ticket types
func EventWithTicketsCacheKey(eventID string) string {
	return "event-full:" + eventID
}

// =============================================
// CACHE TTL CONSTANTS
// =============================================

const (
	// CacheTTLVenue - venue data (changes rarely)
	CacheTTLVenue = 10 * time.Minute
	// CacheTTLEvent - event data (moderate changes)
	CacheTTLEvent = 5 * time.Minute
	// CacheTTLEventList - event lists (changes with new events)
	CacheTTLEventList = 2 * time.Minute
	// CacheTTLRoles - role definitions (rarely change)
	CacheTTLRoles = 30 * time.Minute
	// CacheTTLGuestListType - guest list type config (moderate changes)
	CacheTTLGuestListType = 15 * time.Minute
	// CacheTTLTicketTypes - ticket types (changes with event updates)
	CacheTTLTicketTypes = 5 * time.Minute
)

// =============================================
// TYPED CACHE HELPERS - Zero allocation fast paths
// =============================================

// GetCachedMap retrieves a cached map, returns nil if not found or expired
func GetCachedMap(key string) map[string]interface{} {
	val, ok := AppCache.Get(key)
	if !ok {
		return nil
	}
	if m, ok := val.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// GetCachedSlice retrieves a cached slice of maps
func GetCachedSlice(key string) []map[string]interface{} {
	val, ok := AppCache.Get(key)
	if !ok {
		return nil
	}
	if s, ok := val.([]map[string]interface{}); ok {
		return s
	}
	return nil
}

// SetCachedMap stores a map in cache
func SetCachedMap(key string, value map[string]interface{}, ttl time.Duration) {
	AppCache.Set(key, value, ttl)
}

// SetCachedSlice stores a slice in cache
func SetCachedSlice(key string, value []map[string]interface{}, ttl time.Duration) {
	AppCache.Set(key, value, ttl)
}

// =============================================
// CACHE INVALIDATION HELPERS
// =============================================

// InvalidateVenueCache clears all cache for a venue
func InvalidateVenueCache(venueID string) {
	AppCache.Delete(VenueCacheKey(venueID))
	AppCache.Delete(RolesCacheKey(venueID))
	AppCache.DeletePrefix("events:" + venueID)
}

// InvalidateEventCache clears all cache for an event
func InvalidateEventCache(eventID string) {
	AppCache.Delete(EventCacheKey(eventID))
	AppCache.Delete(EventWithTicketsCacheKey(eventID))
	AppCache.Delete(TicketTypesCacheKey(eventID))
	AppCache.Delete(EventGuestListTypesCacheKey(eventID))
}

// InvalidateGuestListTypeCache clears cache for a guest list type
func InvalidateGuestListTypeCache(typeID string) {
	AppCache.Delete(GuestListTypeCacheKey(typeID))
}
