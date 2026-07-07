package models

import "time"

// =============================================
// JWT CLAIMS
// =============================================

// StaffClaims for venue staff authentication
type StaffClaims struct {
	UserID         string `json:"user_id"`
	Email          string `json:"email"`
	Name           string `json:"name"`
	VenueID        string `json:"venue_id"`
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role"`
	Type           string `json:"type"` // "venue_staff"
}

// UserClaims for public user authentication
type UserClaims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Type   string `json:"type"` // "user"
}

// PlatformClaims for Pull platform staff (legacy - for Authorization header tokens)
type PlatformClaims struct {
	StaffID string `json:"staff_id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"` // admin, support, analyst
	Type    string `json:"type"` // "platform_staff"
}

// PlatformStaffClaims for secure session-based platform auth (cookie tokens)
type PlatformStaffClaims struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`      // admin, support, analyst
	SessionID string `json:"session_id"` // For session tracking
}

// =============================================
// AUTH REQUESTS
// =============================================

// LoginWorkersRequest for staff login
type LoginWorkersRequest struct {
	DPI      string `json:"dpi" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginEmailRequest for email-based login
type LoginEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// VerifyCodeRequest for code verification
type VerifyCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required,len=6"`
}

// GoogleLoginRequest for Google OAuth
type GoogleLoginRequest struct {
	Credential string `json:"credential" binding:"required"`
	Nonce      string `json:"nonce"`
}

// =============================================
// STAFF (Venue DB)
// =============================================

// Staff represents a venue employee
type Staff struct {
	ID             string     `json:"id"`
	VenueID        string     `json:"venue_id"`
	OrganizationID string     `json:"organization_id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Surname        string     `json:"surname"`
	DPI            string     `json:"dpi"`
	Phone          string     `json:"phone,omitempty"`
	Role           string     `json:"role"` // admin, manager, staff, doorman
	IsActive       bool       `json:"is_active"`
	PasswordHash   string     `json:"-"`
	LastLoginAt    *time.Time `json:"last_login_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

// =============================================
// USER (Venue DB - Public users)
// =============================================

// User represents a public app user
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Surname      string     `json:"surname,omitempty"`
	Phone        string     `json:"phone,omitempty"`
	PhonePrefix  string     `json:"phone_prefix,omitempty"`
	BirthDate    string     `json:"birth_date,omitempty"`
	Gender       string     `json:"gender,omitempty"`
	ProfileImage string     `json:"profile_image,omitempty"`
	IsVerified   bool       `json:"is_verified"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// =============================================
// PLATFORM STAFF (Central DB)
// =============================================

// PlatformStaff represents Pull platform employees
type PlatformStaff struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"` // admin, support, analyst
	IsActive     bool       `json:"is_active"`
	PasswordHash string     `json:"-"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
