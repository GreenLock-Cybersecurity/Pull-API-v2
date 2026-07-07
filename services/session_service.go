package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"pull-api-v2/config"
	"pull-api-v2/models"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// =============================================
// SESSION SERVICE - Secure Authentication
// Implements:
// - Access Token (15 min) + Refresh Token (2 days)
// - Refresh Token Rotation
// - Session tracking in pull_staff_sessions
// - HTTP-only Secure Cookies
// =============================================

const (
	// Token expiry times
	AccessTokenExpiry  = 15 * time.Minute
	RefreshTokenExpiry = 48 * time.Hour // 2 days

	// Cookie names
	AccessTokenCookie  = "pull_access"
	RefreshTokenCookie = "pull_refresh"

	// Token types
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// SessionClaims extends JWT claims with session info
type SessionClaims struct {
	jwt.RegisteredClaims
	UserID    string `json:"uid"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role,omitempty"`
	Type      string `json:"type"`       // platform_staff
	TokenType string `json:"token_type"` // access or refresh
	SessionID string `json:"sid"`        // Session ID for tracking
}

// TokenPair represents access + refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ExpiresIn    int64     `json:"expires_in"` // seconds
}

// SessionInfo contains session metadata
type SessionInfo struct {
	ID         string
	StaffID    string
	TokenHash  string
	JTI        string
	IPAddress  string
	UserAgent  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt time.Time
	IsRevoked  bool
}

// =============================================
// TOKEN GENERATION
// =============================================

// GenerateSecureTokenPair creates a new access + refresh token pair for platform staff
func GenerateSecureTokenPair(staff *models.PlatformStaff, sessionID, ipAddress, userAgent string) (*TokenPair, error) {
	now := time.Now()
	jti := uuid.New().String()

	// Generate Access Token (short-lived)
	accessClaims := SessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "pull-api-v2",
			Subject:   staff.ID,
		},
		UserID:    staff.ID,
		Email:     staff.Email,
		Name:      staff.Name,
		Role:      staff.Role,
		Type:      "platform_staff",
		TokenType: TokenTypeAccess,
		SessionID: sessionID,
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(config.App.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Generate Refresh Token (long-lived)
	refreshJTI := uuid.New().String()
	refreshClaims := SessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        refreshJTI,
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "pull-api-v2",
			Subject:   staff.ID,
		},
		UserID:    staff.ID,
		Email:     staff.Email,
		Role:      staff.Role,
		Type:      "platform_staff",
		TokenType: TokenTypeRefresh,
		SessionID: sessionID,
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString([]byte(config.App.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresAt:    now.Add(AccessTokenExpiry),
		ExpiresIn:    int64(AccessTokenExpiry.Seconds()),
	}, nil
}

// ValidateSessionToken validates a token and returns the claims
func ValidateSessionToken(tokenString string) (*SessionClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &SessionClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(config.App.JWTSecret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*SessionClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// =============================================
// SESSION MANAGEMENT (Database)
// =============================================

// CreateSession creates a new session in the database
func CreateSession(ctx context.Context, staffID, refreshToken, ipAddress, userAgent string) (*SessionInfo, error) {
	central := DB.Central()
	if central == nil {
		return nil, fmt.Errorf("central database not available")
	}

	sessionID := uuid.New().String()
	tokenHash := hashToken(refreshToken)
	now := time.Now()
	expiresAt := now.Add(RefreshTokenExpiry)

	// Parse JTI from refresh token
	claims, err := ValidateSessionToken(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}

	// Insert session
	_, err = central.InsertCtx(ctx, "pull_staff_sessions", map[string]interface{}{
		"id":           sessionID,
		"staff_id":     staffID,
		"token_hash":   tokenHash,
		"jti":          claims.ID,
		"expires_at":   expiresAt.Format(time.RFC3339),
		"ip_address":   ipAddress,
		"user_agent":   truncateUserAgent(userAgent),
		"is_revoked":   false,
		"created_at":   now.Format(time.RFC3339),
		"last_used_at": now.Format(time.RFC3339),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &SessionInfo{
		ID:         sessionID,
		StaffID:    staffID,
		TokenHash:  tokenHash,
		JTI:        claims.ID,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		ExpiresAt:  expiresAt,
		CreatedAt:  now,
		LastUsedAt: now,
		IsRevoked:  false,
	}, nil
}

// ValidateSession checks if a session is valid and not revoked
func ValidateSession(ctx context.Context, sessionID, staffID string) (bool, error) {
	central := DB.Central()
	if central == nil {
		return false, fmt.Errorf("central database not available")
	}

	session, err := central.QueryOne(ctx, "pull_staff_sessions", map[string]interface{}{
		"select": "id,is_revoked,expires_at",
		"where": map[string]interface{}{
			"id":       sessionID,
			"staff_id": staffID,
		},
	})

	if err != nil || session == nil {
		return false, nil
	}

	// Check if revoked
	if GetBool(session, "is_revoked") {
		return false, nil
	}

	// Check if expired
	expiresAt := GetTime(session, "expires_at")
	if expiresAt != nil && time.Now().After(*expiresAt) {
		return false, nil
	}

	return true, nil
}

// RefreshSession rotates the refresh token and creates new token pair
func RefreshSession(ctx context.Context, oldRefreshToken, ipAddress, userAgent string) (*TokenPair, error) {
	// Validate old refresh token
	claims, err := ValidateSessionToken(oldRefreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}

	// Must be a refresh token
	if claims.TokenType != TokenTypeRefresh {
		return nil, fmt.Errorf("invalid token type")
	}

	central := DB.Central()
	if central == nil {
		return nil, fmt.Errorf("central database not available")
	}

	// Find and validate session
	oldTokenHash := hashToken(oldRefreshToken)
	session, err := central.QueryOne(ctx, "pull_staff_sessions", map[string]interface{}{
		"select": "id,staff_id,is_revoked,expires_at",
		"where": map[string]interface{}{
			"token_hash": oldTokenHash,
			"staff_id":   claims.UserID,
		},
	})

	if err != nil || session == nil {
		return nil, fmt.Errorf("session not found")
	}

	// Check if session is revoked (possible token reuse attack)
	if GetBool(session, "is_revoked") {
		// SECURITY: Token reuse detected - revoke all sessions for this user
		go revokeAllUserSessions(claims.UserID)
		return nil, fmt.Errorf("session revoked - possible token reuse attack")
	}

	// Check if expired
	expiresAt := GetTime(session, "expires_at")
	if expiresAt != nil && time.Now().After(*expiresAt) {
		return nil, fmt.Errorf("session expired")
	}

	sessionID := GetString(session, "id")
	staffID := GetString(session, "staff_id")

	// Get staff info for new tokens
	staff, err := central.QueryOne(ctx, "pull_staff", map[string]interface{}{
		"select": "id,email,name,role,is_active",
		"where": map[string]interface{}{
			"id": staffID,
		},
	})

	if err != nil || staff == nil {
		return nil, fmt.Errorf("staff not found")
	}

	// Check if staff is still active
	if !GetBool(staff, "is_active") {
		return nil, fmt.Errorf("account is disabled")
	}

	// Generate new token pair
	platformStaff := &models.PlatformStaff{
		ID:    staffID,
		Email: GetString(staff, "email"),
		Name:  GetString(staff, "name"),
		Role:  GetString(staff, "role"),
	}

	newTokenPair, err := GenerateSecureTokenPair(platformStaff, sessionID, ipAddress, userAgent)
	if err != nil {
		return nil, fmt.Errorf("failed to generate new tokens: %w", err)
	}

	// ROTATION: Revoke old token and update with new one
	newTokenHash := hashToken(newTokenPair.RefreshToken)
	newClaims, _ := ValidateSessionToken(newTokenPair.RefreshToken)

	now := time.Now()
	_, err = central.UpdateCtx(ctx, "pull_staff_sessions", map[string]interface{}{
		"token_hash":   newTokenHash,
		"jti":          newClaims.ID,
		"expires_at":   now.Add(RefreshTokenExpiry).Format(time.RFC3339),
		"ip_address":   ipAddress,
		"user_agent":   truncateUserAgent(userAgent),
		"last_used_at": now.Format(time.RFC3339),
	}, map[string]interface{}{
		"id": sessionID,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to update session: %w", err)
	}

	return newTokenPair, nil
}

// RevokeSession revokes a specific session
func RevokeSession(ctx context.Context, sessionID, staffID string) error {
	central := DB.Central()
	if central == nil {
		return fmt.Errorf("central database not available")
	}

	now := time.Now()
	_, err := central.UpdateCtx(ctx, "pull_staff_sessions", map[string]interface{}{
		"is_revoked": true,
		"revoked_at": now.Format(time.RFC3339),
	}, map[string]interface{}{
		"id":       sessionID,
		"staff_id": staffID,
	})

	return err
}

// RevokeAllSessions revokes all sessions for a user
func RevokeAllSessions(ctx context.Context, staffID string) error {
	central := DB.Central()
	if central == nil {
		return fmt.Errorf("central database not available")
	}

	now := time.Now()
	_, err := central.UpdateCtx(ctx, "pull_staff_sessions", map[string]interface{}{
		"is_revoked": true,
		"revoked_at": now.Format(time.RFC3339),
	}, map[string]interface{}{
		"staff_id":   staffID,
		"is_revoked": false,
	})

	return err
}

// UpdateSessionActivity updates the last_used_at timestamp
func UpdateSessionActivity(ctx context.Context, sessionID string) {
	central := DB.Central()
	if central == nil {
		return
	}

	central.UpdateNoReturn(ctx, "pull_staff_sessions", map[string]interface{}{
		"last_used_at": time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": sessionID,
	})
}

// GetActiveSessions returns all active sessions for a user
func GetActiveSessions(ctx context.Context, staffID string) ([]map[string]interface{}, error) {
	central := DB.Central()
	if central == nil {
		return nil, fmt.Errorf("central database not available")
	}

	sessions, err := central.QueryCtx(ctx, "pull_staff_sessions", map[string]interface{}{
		"select": "id,ip_address,user_agent,created_at,last_used_at,expires_at",
		"where": map[string]interface{}{
			"staff_id":   staffID,
			"is_revoked": false,
		},
		"order": "last_used_at.desc",
	})

	if err != nil {
		return nil, err
	}

	// Filter out expired sessions
	var activeSessions []map[string]interface{}
	now := time.Now()
	for _, session := range sessions {
		expiresAt := GetTime(session, "expires_at")
		if expiresAt != nil && now.Before(*expiresAt) {
			activeSessions = append(activeSessions, session)
		}
	}

	return activeSessions, nil
}

// =============================================
// HELPER FUNCTIONS
// =============================================

// hashToken creates a SHA-256 hash of the token for storage
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// generateSecureBytes generates cryptographically secure random bytes
func generateSecureBytes(n int) ([]byte, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}

// truncateUserAgent limits user agent string to 500 chars (DB constraint)
func truncateUserAgent(ua string) string {
	if len(ua) > 500 {
		return ua[:500]
	}
	return ua
}

// revokeAllUserSessions is called when token reuse is detected
func revokeAllUserSessions(staffID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	central := DB.Central()
	if central == nil {
		return
	}

	now := time.Now()
	central.UpdateNoReturn(ctx, "pull_staff_sessions", map[string]interface{}{
		"is_revoked": true,
		"revoked_at": now.Format(time.RFC3339),
	}, map[string]interface{}{
		"staff_id":   staffID,
		"is_revoked": false,
	})

	// Log security event
	central.InsertCtx(ctx, "pull_staff_audit_log", map[string]interface{}{
		"staff_id":    staffID,
		"action":      "security_token_reuse_detected",
		"metadata":    map[string]interface{}{"all_sessions_revoked": true},
		"created_at":  now.Format(time.RFC3339),
	})
}

// CleanupExpiredSessions removes expired sessions (call periodically)
func CleanupExpiredSessions(ctx context.Context) (int, error) {
	central := DB.Central()
	if central == nil {
		return 0, fmt.Errorf("central database not available")
	}

	// Delete sessions expired more than 7 days ago
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	// Use raw query for DELETE with condition
	result, err := central.QueryCtx(ctx, "pull_staff_sessions", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"expires_at": fmt.Sprintf("lt.%s", cutoff.Format(time.RFC3339)),
		},
	})

	if err != nil {
		return 0, err
	}

	// Delete each expired session
	count := 0
	for _, session := range result {
		sessionID := GetString(session, "id")
		if sessionID != "" {
			central.DeleteCtx(ctx, "pull_staff_sessions", map[string]interface{}{
				"id": sessionID,
			})
			count++
		}
	}

	return count, nil
}
