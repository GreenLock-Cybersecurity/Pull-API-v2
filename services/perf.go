package services

import (
	"sync"

	"github.com/gin-gonic/gin"
)

// =============================================
// PERFORMANCE UTILITIES
// Object pooling and pre-allocation for hot paths
// =============================================

// =============================================
// SYNC.POOL FOR COMMON OBJECTS
// =============================================

// ResponseMapPool pools gin.H objects to reduce allocations
var ResponseMapPool = sync.Pool{
	New: func() interface{} {
		return make(gin.H, 16) // Pre-allocate for typical response
	},
}

// GetResponseMap gets a pooled gin.H map
func GetResponseMap() gin.H {
	return ResponseMapPool.Get().(gin.H)
}

// PutResponseMap returns a gin.H map to the pool after clearing
func PutResponseMap(m gin.H) {
	for k := range m {
		delete(m, k)
	}
	ResponseMapPool.Put(m)
}

// SliceMapPool pools []map[string]interface{} slices
var SliceMapPool = sync.Pool{
	New: func() interface{} {
		s := make([]map[string]interface{}, 0, 32)
		return &s
	},
}

// GetSliceMap gets a pooled slice of maps
func GetSliceMap() *[]map[string]interface{} {
	return SliceMapPool.Get().(*[]map[string]interface{})
}

// PutSliceMap returns a slice to the pool after clearing
func PutSliceMap(s *[]map[string]interface{}) {
	*s = (*s)[:0]
	SliceMapPool.Put(s)
}

// StringSlicePool pools string slices
var StringSlicePool = sync.Pool{
	New: func() interface{} {
		s := make([]string, 0, 64)
		return &s
	},
}

// GetStringSlice gets a pooled string slice
func GetStringSlice() *[]string {
	return StringSlicePool.Get().(*[]string)
}

// PutStringSlice returns a string slice to the pool
func PutStringSlice(s *[]string) {
	*s = (*s)[:0]
	StringSlicePool.Put(s)
}

// MapPool pools map[string]interface{}
var MapPool = sync.Pool{
	New: func() interface{} {
		return make(map[string]interface{}, 16)
	},
}

// GetMap gets a pooled map
func GetMap() map[string]interface{} {
	return MapPool.Get().(map[string]interface{})
}

// PutMap returns a map to the pool after clearing
func PutMap(m map[string]interface{}) {
	for k := range m {
		delete(m, k)
	}
	MapPool.Put(m)
}

// =============================================
// STRING MAP POOL FOR ID LOOKUPS
// =============================================

// StringMapPool pools map[string]string for ID mappings
var StringMapPool = sync.Pool{
	New: func() interface{} {
		return make(map[string]string, 64)
	},
}

// GetStringMap gets a pooled string map
func GetStringMap() map[string]string {
	return StringMapPool.Get().(map[string]string)
}

// PutStringMap returns a string map to the pool
func PutStringMap(m map[string]string) {
	for k := range m {
		delete(m, k)
	}
	StringMapPool.Put(m)
}

// =============================================
// BATCH HELPERS WITH PRE-ALLOCATION
// =============================================

// BatchResult holds results from batch operations
type BatchResult struct {
	Data  []map[string]interface{}
	Error error
}

// NewBatchResult creates a batch result with pre-allocated slice
func NewBatchResult(capacity int) *BatchResult {
	return &BatchResult{
		Data: make([]map[string]interface{}, 0, capacity),
	}
}

// =============================================
// CONCURRENT MAP FOR SAFE WRITES
// =============================================

// ConcurrentMap is a thread-safe map for building results
type ConcurrentMap struct {
	mu   sync.RWMutex
	data map[string]map[string]interface{}
}

// NewConcurrentMap creates a new concurrent map
func NewConcurrentMap(capacity int) *ConcurrentMap {
	return &ConcurrentMap{
		data: make(map[string]map[string]interface{}, capacity),
	}
}

// Set adds a key-value pair
func (cm *ConcurrentMap) Set(key string, value map[string]interface{}) {
	cm.mu.Lock()
	cm.data[key] = value
	cm.mu.Unlock()
}

// Get retrieves a value by key
func (cm *ConcurrentMap) Get(key string) (map[string]interface{}, bool) {
	cm.mu.RLock()
	val, ok := cm.data[key]
	cm.mu.RUnlock()
	return val, ok
}

// GetAll returns all data (not thread-safe after this call)
func (cm *ConcurrentMap) GetAll() map[string]map[string]interface{} {
	return cm.data
}

// Len returns the number of entries
func (cm *ConcurrentMap) Len() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.data)
}

// =============================================
// PARALLEL BATCH PROCESSOR
// =============================================

// ParallelBatchProcessor processes items in parallel with concurrency limit
type ParallelBatchProcessor struct {
	concurrency int
	sem         chan struct{}
}

// NewParallelBatchProcessor creates a new processor
func NewParallelBatchProcessor(concurrency int) *ParallelBatchProcessor {
	if concurrency <= 0 {
		concurrency = 10
	}
	return &ParallelBatchProcessor{
		concurrency: concurrency,
		sem:         make(chan struct{}, concurrency),
	}
}

// Process processes items in parallel
func (p *ParallelBatchProcessor) Process(
	items []string,
	processor func(item string) (map[string]interface{}, error),
) *ConcurrentMap {
	result := NewConcurrentMap(len(items))
	var wg sync.WaitGroup

	for _, item := range items {
		wg.Add(1)
		p.sem <- struct{}{} // Acquire

		go func(id string) {
			defer wg.Done()
			defer func() { <-p.sem }() // Release

			if data, err := processor(id); err == nil && data != nil {
				result.Set(id, data)
			}
		}(item)
	}

	wg.Wait()
	return result
}

// =============================================
// RESPONSE BUILDER - ZERO ALLOC WHERE POSSIBLE
// =============================================

// ResponseBuilder builds JSON responses efficiently
type ResponseBuilder struct {
	data gin.H
}

// NewResponseBuilder creates a new response builder
func NewResponseBuilder() *ResponseBuilder {
	return &ResponseBuilder{
		data: GetResponseMap(),
	}
}

// Set adds a field to the response
func (rb *ResponseBuilder) Set(key string, value interface{}) *ResponseBuilder {
	rb.data[key] = value
	return rb
}

// Success sets success=true and adds message
func (rb *ResponseBuilder) Success(message string) *ResponseBuilder {
	rb.data["success"] = true
	if message != "" {
		rb.data["message"] = message
	}
	return rb
}

// WithData adds a data field
func (rb *ResponseBuilder) WithData(data interface{}) *ResponseBuilder {
	rb.data["data"] = data
	return rb
}

// Build returns the response map (caller must call Release when done)
func (rb *ResponseBuilder) Build() gin.H {
	return rb.data
}

// Release returns the map to the pool (call after response is sent)
// Note: In practice, gin's c.JSON copies the data, so this is safe after c.JSON
func (rb *ResponseBuilder) Release() {
	PutResponseMap(rb.data)
}
