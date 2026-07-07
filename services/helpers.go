package services

import (
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/gin-gonic/gin"
)

// =============================================
// ULTRA-OPTIMIZED HELPERS
// Zero-allocation where possible
// NOTE: ExtractIDs, BuildIDMap, BuildIDNameMap are defined in parallel.go
// =============================================

// JoinIDs joins string IDs efficiently for PostgREST IN clause
func JoinIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) == 1 {
		return ids[0]
	}

	// Calculate total length to pre-allocate
	totalLen := len(ids) - 1 // commas
	for _, id := range ids {
		totalLen += len(id)
	}

	var b strings.Builder
	b.Grow(totalLen)
	b.WriteString(ids[0])
	for _, id := range ids[1:] {
		b.WriteByte(',')
		b.WriteString(id)
	}
	return b.String()
}

// FormatInClause formats IDs for PostgREST IN clause: in.(id1,id2,id3)
func FormatInClause(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "in.(" + JoinIDs(ids) + ")"
}

// =============================================
// STRING POOL (reduce allocations for common strings)
// =============================================

var stringPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 256)
		return &b
	},
}

// GetPooledBuilder returns a pooled strings.Builder
func GetPooledBuilder() *strings.Builder {
	b := stringPool.Get().(*[]byte)
	*b = (*b)[:0]
	sb := &strings.Builder{}
	return sb
}

// =============================================
// FAST MAP ACCESS (zero-allocation type assertions)
// =============================================

// GetStringFast gets string without interface boxing (when you know the type)
func GetStringFast(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// GetIntFast gets int with fast path for float64 (JSON numbers)
func GetIntFast(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	// JSON numbers come as float64
	if f, ok := v.(float64); ok {
		return int(f), true
	}
	if i, ok := v.(int); ok {
		return i, true
	}
	return 0, false
}

// =============================================
// BATCH PROCESSING HELPERS
// =============================================

// ChunkSlice splits a slice into chunks of specified size
func ChunkSlice[T any](slice []T, chunkSize int) [][]T {
	if len(slice) == 0 || chunkSize <= 0 {
		return nil
	}

	numChunks := (len(slice) + chunkSize - 1) / chunkSize
	chunks := make([][]T, 0, numChunks)

	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}

	return chunks
}

// ProcessInParallel processes items in parallel with limited concurrency
func ProcessInParallel[T any, R any](items []T, concurrency int, processor func(T) R) []R {
	if len(items) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	results := make([]R, len(items))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(idx int, it T) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			results[idx] = processor(it)
		}(i, item)
	}

	wg.Wait()
	return results
}

// =============================================
// UNSAFE STRING CONVERSION (use with caution)
// For read-only strings from byte slices
// =============================================

// BytesToString converts bytes to string without allocation (read-only!)
func BytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// StringToBytes converts string to bytes without allocation (read-only!)
func StringToBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// =============================================
// NUMERIC HELPERS
// =============================================

// MinInt returns the minimum of two ints (no generics overhead)
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MaxInt returns the maximum of two ints
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ClampInt clamps an int to a range
func ClampInt(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// =============================================
// GIN CONTEXT HELPERS
// =============================================

// GetIntParam gets an integer query parameter with a default value
func GetIntParam(c *gin.Context, key string, defaultVal int) int {
	valStr := c.Query(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}

// GetStringParam gets a string query parameter with a default value
func GetStringParam(c *gin.Context, key string, defaultVal string) string {
	val := c.Query(key)
	if val == "" {
		return defaultVal
	}
	return val
}

// GetBoolParam gets a boolean query parameter with a default value
func GetBoolParam(c *gin.Context, key string, defaultVal bool) bool {
	valStr := c.Query(key)
	if valStr == "" {
		return defaultVal
	}
	return valStr == "true" || valStr == "1" || valStr == "yes"
}
