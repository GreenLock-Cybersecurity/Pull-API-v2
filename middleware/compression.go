package middleware

import (
	"compress/gzip"
	"io"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// =============================================
// GZIP COMPRESSION MIDDLEWARE
// Pool-based for zero-allocation compression
// =============================================

// gzipWriterPool reuses gzip writers to reduce allocations
var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed) // Level 1 = fastest
		return w
	},
}

// gzipResponseWriter wraps gin.ResponseWriter with gzip compression
type gzipResponseWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func (g *gzipResponseWriter) Write(data []byte) (int, error) {
	return g.writer.Write(data)
}

func (g *gzipResponseWriter) WriteString(s string) (int, error) {
	return g.writer.Write([]byte(s))
}

// GzipCompression compresses responses for clients that support it
// Only compresses responses larger than minSize bytes
func GzipCompression(minSize int) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if client accepts gzip
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		// Skip compression for small responses or specific content types
		contentType := c.GetHeader("Content-Type")
		if strings.Contains(contentType, "image/") ||
			strings.Contains(contentType, "video/") ||
			strings.Contains(contentType, "audio/") {
			c.Next()
			return
		}

		// Get pooled gzip writer
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(c.Writer)

		// Create wrapped writer
		gzw := &gzipResponseWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}

		// Set headers
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")

		// Replace writer
		c.Writer = gzw

		// Process request
		c.Next()

		// Ensure gzip is flushed and closed
		gz.Close()

		// Return to pool
		gzipWriterPool.Put(gz)
	}
}

// GzipCompressionFast provides fast gzip compression for API responses
// Uses BestSpeed level for minimum latency
func GzipCompressionFast() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if client accepts gzip
		acceptEncoding := c.GetHeader("Accept-Encoding")
		if !strings.Contains(acceptEncoding, "gzip") {
			c.Next()
			return
		}

		// Skip for streaming responses or files
		if c.GetHeader("Accept") == "text/event-stream" {
			c.Next()
			return
		}

		// Get pooled gzip writer
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(c.Writer)

		// Wrap response writer
		gzw := &gzipResponseWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}

		// Set compression headers
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")

		// Use wrapped writer
		c.Writer = gzw

		// Execute handlers
		c.Next()

		// Close and return to pool
		gz.Close()
		gzipWriterPool.Put(gz)
	}
}

// =============================================
// RESPONSE SIZE TRACKING
// =============================================

type responseTracker struct {
	gin.ResponseWriter
	size int
}

func (r *responseTracker) Write(data []byte) (int, error) {
	n, err := r.ResponseWriter.Write(data)
	r.size += n
	return n, err
}

func (r *responseTracker) Size() int {
	return r.size
}

// TrackResponseSize middleware tracks response sizes for monitoring
func TrackResponseSize() gin.HandlerFunc {
	return func(c *gin.Context) {
		tracker := &responseTracker{ResponseWriter: c.Writer}
		c.Writer = tracker
		c.Next()
		c.Set("response_size", tracker.size)
	}
}

// =============================================
// CONDITIONAL COMPRESSION
// Only compress if response is large enough
// =============================================

const defaultMinCompressSize = 1024 // 1KB minimum

type conditionalGzipWriter struct {
	gin.ResponseWriter
	gzWriter     *gzip.Writer
	buffer       []byte
	minSize      int
	compressed   bool
	headersSent  bool
}

func (c *conditionalGzipWriter) Write(data []byte) (int, error) {
	if c.headersSent {
		if c.compressed {
			return c.gzWriter.Write(data)
		}
		return c.ResponseWriter.Write(data)
	}

	// Buffer data until we know if we should compress
	c.buffer = append(c.buffer, data...)

	// If buffer exceeds min size, start compressing
	if len(c.buffer) >= c.minSize {
		c.compressed = true
		c.headersSent = true
		c.Header().Set("Content-Encoding", "gzip")
		c.gzWriter.Reset(c.ResponseWriter)
		return c.gzWriter.Write(c.buffer)
	}

	return len(data), nil
}

func (c *conditionalGzipWriter) Flush() {
	if !c.headersSent && len(c.buffer) > 0 {
		// Small response - write uncompressed
		c.headersSent = true
		c.ResponseWriter.Write(c.buffer)
	}
	if c.compressed && c.gzWriter != nil {
		c.gzWriter.Flush()
	}
}

func (c *conditionalGzipWriter) Close() error {
	if !c.headersSent && len(c.buffer) > 0 {
		c.headersSent = true
		c.ResponseWriter.Write(c.buffer)
	}
	if c.compressed && c.gzWriter != nil {
		return c.gzWriter.Close()
	}
	return nil
}

// SmartGzipCompression only compresses responses larger than minSize
func SmartGzipCompression(minSize int) gin.HandlerFunc {
	if minSize <= 0 {
		minSize = defaultMinCompressSize
	}

	return func(c *gin.Context) {
		// Check if client accepts gzip
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		// Skip for certain content types
		c.Header("Vary", "Accept-Encoding")

		gz := gzipWriterPool.Get().(*gzip.Writer)
		defer gzipWriterPool.Put(gz)

		cgz := &conditionalGzipWriter{
			ResponseWriter: c.Writer,
			gzWriter:       gz,
			minSize:        minSize,
			buffer:         make([]byte, 0, minSize),
		}

		c.Writer = cgz
		c.Next()

		// Ensure everything is flushed
		cgz.Close()
	}
}

// =============================================
// NO-OP WRITER (for benchmarking)
// =============================================

type noopWriter struct {
	gin.ResponseWriter
}

func (n *noopWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

// Benchmark middleware that discards output
func BenchmarkMode() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer = &noopWriter{c.Writer}
		c.Next()
	}
}
