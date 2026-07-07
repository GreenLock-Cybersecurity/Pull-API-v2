package middleware

import (
	"context"
	"net/http"
	"pull-api-v2/models"
	"pull-api-v2/services"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================
// JWT TOKEN CACHE (CRITICAL PERFORMANCE OPTIMIZATION)
// Caches validated tokens to avoid CPU-intensive HMAC
// signature verification on every request
// =============================================

type tokenCacheEntry struct {
	claims    *services.JWTClaims
	expiresAt time.Time
}

var (
	jwtCache   = make(map[string]*tokenCacheEntry)
	jwtCacheMu sync.RWMutex
	jwtCacheTTL = 5 * time.Minute // Cache for 5 minutes (shorter than token lifetime)
)

// validateTokenCached validates a JWT token with caching
// Returns cached claims if available, otherwise validates and caches
func validateTokenCached(token string) (*services.JWTClaims, error) {
	// Fast path: check cache with read lock
	jwtCacheMu.RLock()
	if entry, ok := jwtCache[token]; ok && time.Now().Before(entry.expiresAt) {
		jwtCacheMu.RUnlock()
		return entry.claims, nil
	}
	jwtCacheMu.RUnlock()

	// Slow path: validate token (CPU-intensive HMAC verification)
	claims, err := services.ValidateToken(token)
	if err != nil {
		return nil, err
	}

	// Cache the validated token
	jwtCacheMu.Lock()
	jwtCache[token] = &tokenCacheEntry{
		claims:    claims,
		expiresAt: time.Now().Add(jwtCacheTTL),
	}
	jwtCacheMu.Unlock()

	return claims, nil
}

// startJWTCacheCleanup starts background cleanup of expired cache entries
func startJWTCacheCleanup() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanJWTCache()
		}
	}()
}

// cleanJWTCache removes expired entries from the JWT cache
func cleanJWTCache() {
	now := time.Now()
	jwtCacheMu.Lock()
	for token, entry := range jwtCache {
		if now.After(entry.expiresAt) {
			delete(jwtCache, token)
		}
	}
	jwtCacheMu.Unlock()
}

func init() {
	startJWTCacheCleanup()
}

// =============================================
// AUTHENTICATION MIDDLEWARE
// =============================================

// AuthenticateStaff validates venue staff JWT tokens
func AuthenticateStaff() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		// Use cached validation for performance
		claims, err := validateTokenCached(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		if claims.Type != "venue_staff" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Staff access required"})
			c.Abort()
			return
		}

		// Set claims in context
		c.Set("claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("staff_id", claims.UserID) // Also set staff_id for convenience
		c.Set("venue_id", claims.VenueID)
		c.Set("organization_id", claims.OrganizationID)
		c.Set("role", claims.Role)
		c.Set("email", claims.Email)
		c.Set("name", claims.Name)

		// Set typed staff claims for controllers
		c.Set("staff", &models.StaffClaims{
			UserID:         claims.UserID,
			Email:          claims.Email,
			Name:           claims.Name,
			VenueID:        claims.VenueID,
			OrganizationID: claims.OrganizationID,
			Role:           claims.Role,
			Type:           claims.Type,
		})

		c.Next()
	}
}

// AuthenticateUser validates public user JWT tokens
func AuthenticateUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		// Use cached validation for performance
		claims, err := validateTokenCached(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		if claims.Type != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "User access required"})
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)

		// Set typed user claims for controllers
		c.Set("user", &models.UserClaims{
			UserID: claims.UserID,
			Email:  claims.Email,
			Type:   claims.Type,
		})

		c.Next()
	}
}

// AuthenticatePlatform validates Pull platform staff JWT tokens (legacy - Authorization header)
func AuthenticatePlatform() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		// Use cached validation for performance
		claims, err := validateTokenCached(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		if claims.Type != "platform_staff" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Platform staff access required"})
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Set("staff_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)

		c.Next()
	}
}

// =============================================
// SECURE PLATFORM AUTH (Cookie-based with sessions)
// =============================================

// sessionCacheEntry for caching session validation
type sessionCacheEntry struct {
	claims    *services.SessionClaims
	valid     bool
	expiresAt time.Time
}

var (
	sessionCache   = make(map[string]*sessionCacheEntry)
	sessionCacheMu sync.RWMutex
	sessionCacheTTL = 2 * time.Minute // Short TTL for session cache
)

// validateSessionCached validates a session token with caching
func validateSessionCached(token string) (*services.SessionClaims, error) {
	// Fast path: check cache with read lock
	sessionCacheMu.RLock()
	if entry, ok := sessionCache[token]; ok && time.Now().Before(entry.expiresAt) {
		sessionCacheMu.RUnlock()
		if !entry.valid {
			return nil, nil
		}
		return entry.claims, nil
	}
	sessionCacheMu.RUnlock()

	// Slow path: validate token
	claims, err := services.ValidateSessionToken(token)
	if err != nil {
		// Cache negative result briefly to prevent repeated validation attempts
		sessionCacheMu.Lock()
		sessionCache[token] = &sessionCacheEntry{
			valid:     false,
			expiresAt: time.Now().Add(30 * time.Second),
		}
		sessionCacheMu.Unlock()
		return nil, err
	}

	// Cache the validated token
	sessionCacheMu.Lock()
	sessionCache[token] = &sessionCacheEntry{
		claims:    claims,
		valid:     true,
		expiresAt: time.Now().Add(sessionCacheTTL),
	}
	sessionCacheMu.Unlock()

	return claims, nil
}

// startSessionCacheCleanup starts background cleanup
func startSessionCacheCleanup() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanSessionCache()
		}
	}()
}

// cleanSessionCache removes expired entries
func cleanSessionCache() {
	now := time.Now()
	sessionCacheMu.Lock()
	for token, entry := range sessionCache {
		if now.After(entry.expiresAt) {
			delete(sessionCache, token)
		}
	}
	sessionCacheMu.Unlock()
}

func init() {
	startSessionCacheCleanup()
}

// AuthenticatePlatformSecure validates platform staff with secure cookies and session tracking
func AuthenticatePlatformSecure() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try to get access token from cookie first
		accessToken, err := c.Cookie(services.AccessTokenCookie)
		if err != nil || accessToken == "" {
			// Try Authorization header as fallback (for API clients)
			accessToken = extractToken(c)
		}

		if accessToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Authentication required",
				"code":  "NO_TOKEN",
			})
			c.Abort()
			return
		}

		// Validate access token with caching
		claims, err := validateSessionCached(accessToken)
		if err != nil || claims == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid or expired token",
				"code":  "INVALID_TOKEN",
			})
			c.Abort()
			return
		}

		// Must be an access token
		if claims.TokenType != services.TokenTypeAccess {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid token type",
				"code":  "WRONG_TOKEN_TYPE",
			})
			c.Abort()
			return
		}

		// Must be platform staff
		if claims.Type != "platform_staff" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Platform staff access required",
				"code":  "FORBIDDEN",
			})
			c.Abort()
			return
		}

		// Validate session in database (async check - don't block for performance)
		// For high-security operations, call ValidateSession explicitly
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			valid, _ := services.ValidateSession(ctx, claims.SessionID, claims.UserID)
			if !valid {
				// Invalidate cache entry if session is revoked
				sessionCacheMu.Lock()
				delete(sessionCache, accessToken)
				sessionCacheMu.Unlock()
			}
		}()

		// Set claims in context
		c.Set("claims", claims)
		c.Set("staff_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("session_id", claims.SessionID)

		// Set typed platform claims for controllers
		c.Set("platform_staff", &models.PlatformStaffClaims{
			UserID:    claims.UserID,
			Email:     claims.Email,
			Name:      claims.Name,
			Role:      claims.Role,
			SessionID: claims.SessionID,
		})

		c.Next()
	}
}

// AuthenticatePlatformSecureStrict validates with synchronous session check
// Use this for sensitive operations (logout, password change, etc.)
func AuthenticatePlatformSecureStrict() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try to get access token from cookie first
		accessToken, err := c.Cookie(services.AccessTokenCookie)
		if err != nil || accessToken == "" {
			accessToken = extractToken(c)
		}

		if accessToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Authentication required",
				"code":  "NO_TOKEN",
			})
			c.Abort()
			return
		}

		// Validate access token (no cache for strict mode)
		claims, err := services.ValidateSessionToken(accessToken)
		if err != nil || claims == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid or expired token",
				"code":  "INVALID_TOKEN",
			})
			c.Abort()
			return
		}

		if claims.TokenType != services.TokenTypeAccess || claims.Type != "platform_staff" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Invalid access",
				"code":  "FORBIDDEN",
			})
			c.Abort()
			return
		}

		// STRICT: Synchronous session validation
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		valid, err := services.ValidateSession(ctx, claims.SessionID, claims.UserID)
		if !valid || err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Session expired or revoked",
				"code":  "SESSION_INVALID",
			})
			c.Abort()
			return
		}

		// Set claims in context
		c.Set("claims", claims)
		c.Set("staff_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("session_id", claims.SessionID)

		c.Set("platform_staff", &models.PlatformStaffClaims{
			UserID:    claims.UserID,
			Email:     claims.Email,
			Name:      claims.Name,
			Role:      claims.Role,
			SessionID: claims.SessionID,
		})

		c.Next()
	}
}

// OptionalAuth tries to authenticate but continues if no token
func OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.Next()
			return
		}

		// Use cached validation for performance
		claims, err := validateTokenCached(token)
		if err != nil {
			c.Next()
			return
		}

		c.Set("claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)

		c.Next()
	}
}

// extractToken extracts JWT from Authorization header or cookie
// OPTIMIZED: Zero allocation for common case (Bearer token)
func extractToken(c *gin.Context) string {
	// Try Authorization header first
	authHeader := c.GetHeader("Authorization")
	if len(authHeader) > 7 {
		// Case-insensitive "Bearer " check without allocation
		// Most APIs send "Bearer" with capital B
		if (authHeader[0] == 'B' || authHeader[0] == 'b') &&
			(authHeader[1] == 'e' || authHeader[1] == 'E') &&
			(authHeader[2] == 'a' || authHeader[2] == 'A') &&
			(authHeader[3] == 'r' || authHeader[3] == 'R') &&
			(authHeader[4] == 'e' || authHeader[4] == 'E') &&
			(authHeader[5] == 'r' || authHeader[5] == 'R') &&
			authHeader[6] == ' ' {
			return authHeader[7:]
		}
	}

	// Try cookie as fallback
	if token, err := c.Cookie("auth_token"); err == nil && token != "" {
		return token
	}

	return ""
}

// =============================================
// HELPER FUNCTIONS
// =============================================

// GetVenueID extracts venue_id from context (set by staff auth)
func GetVenueID(c *gin.Context) string {
	if venueID, exists := c.Get("venue_id"); exists {
		return venueID.(string)
	}
	return ""
}

// GetUserID extracts user_id from context
func GetUserID(c *gin.Context) string {
	if userID, exists := c.Get("user_id"); exists {
		return userID.(string)
	}
	return ""
}

// GetOrganizationID extracts organization_id from context
func GetOrganizationID(c *gin.Context) string {
	if orgID, exists := c.Get("organization_id"); exists {
		return orgID.(string)
	}
	return ""
}

// GetRole extracts role from context
func GetRole(c *gin.Context) string {
	if role, exists := c.Get("role"); exists {
		return role.(string)
	}
	return ""
}

// GetClaims extracts full claims from context
func GetClaims(c *gin.Context) *services.JWTClaims {
	if claims, exists := c.Get("claims"); exists {
		return claims.(*services.JWTClaims)
	}
	return nil
}
