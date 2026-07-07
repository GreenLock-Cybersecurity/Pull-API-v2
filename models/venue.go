package models

import "time"

// =============================================
// VENUE (Central DB - Platform data)
// Matches pull-central.venues table
// =============================================

// =============================================
// VENUE FLOW TYPES
// Determines the primary operational mode of the venue
// =============================================

type VenueFlowType string

const (
	FlowTypeVIPList   VenueFlowType = "vip_list"   // Primarily VIP list reservations (clubs, lounges)
	FlowTypeStandard  VenueFlowType = "standard"   // Individual tickets + group reservations
	FlowTypeGuestList VenueFlowType = "guest_list" // Guest list focused (exclusive events)
	FlowTypeHybrid    VenueFlowType = "hybrid"     // All features enabled
)

// =============================================
// VENUE FEATURES - Enhanced Feature Flag System
// Configurable per venue, stored in central DB
// =============================================

// VenueFeatures defines all configurable features for a venue
type VenueFeatures struct {
	// Primary flow type - determines default UI behavior
	FlowType VenueFlowType `json:"flow_type"`

	// Enabled modules - what's available in this venue
	EnabledModules []string `json:"enabled_modules"` // ["vip_lists", "guest_lists", "individual_tickets", "group_reservations", "bottle_service"]

	// UI Configuration
	DefaultTab     string `json:"default_tab"`      // Which tab to show first in mobile app
	ShowAllTabs    bool   `json:"show_all_tabs"`    // Show all tabs or only relevant ones
	BrandingColor  string `json:"branding_color"`   // Custom accent color for the venue

	// Booking behavior
	RequiresApproval         bool `json:"requires_approval"`           // Orders need staff approval
	RequiresApprovalGuestList bool `json:"requires_approval_guest_list"` // Guest list signups need approval
	RequiresApprovalVIPList  bool `json:"requires_approval_vip_list"`  // VIP lists need approval

	// Pricing features
	GenderBasedPricing bool    `json:"gender_based_pricing"` // Different prices for male/female
	ServiceFeePercent  float64 `json:"service_fee_percent"`  // Additional service fee (e.g., 15%)

	// VIP List specific
	BottleService          bool `json:"bottle_service"`            // Venue offers bottle service
	PaymentDeadlineHours   int  `json:"payment_deadline_hours"`    // Default deadline for guest payments (e.g., 48)
	AllowGuestSelfPayment  bool `json:"allow_guest_self_payment"`  // Guests can pay individually
	AllowHostPayAll        bool `json:"allow_host_pay_all"`        // Host can pay for all guests
	MinGuestsPerVIPList    int  `json:"min_guests_per_vip_list"`   // Minimum guests required
	MaxGuestsPerVIPList    int  `json:"max_guests_per_vip_list"`   // Maximum guests allowed

	// Guest List specific
	GuestListAutoApprove   bool `json:"guest_list_auto_approve"`   // Auto-approve guest list signups
	GuestListCapacityLimit int  `json:"guest_list_capacity_limit"` // Max signups per guest list type

	// Ticket features
	QRCodeRequired     bool `json:"qr_code_required"`      // Require QR code for entry
	AllowWalkIns       bool `json:"allow_walk_ins"`        // Allow manual walk-in tickets
	AllowTransfers     bool `json:"allow_transfers"`       // Allow ticket transfers between users
	AllowResale        bool `json:"allow_resale"`          // Allow ticket resale

	// Check-in features
	MultiScanAllowed   bool `json:"multi_scan_allowed"`    // Allow same ticket to be scanned multiple times
	CheckInWindowHours int  `json:"check_in_window_hours"` // Hours before event to allow check-in

	// Notifications
	SendWhatsApp       bool `json:"send_whatsapp"`         // Send WhatsApp notifications
	SendEmail          bool `json:"send_email"`            // Send email notifications
	SendPush           bool `json:"send_push"`             // Send push notifications
}

// DefaultVenueFeatures returns sensible defaults for a new venue
func DefaultVenueFeatures() VenueFeatures {
	return VenueFeatures{
		FlowType:                  FlowTypeStandard,
		EnabledModules:            []string{"individual_tickets", "guest_lists"},
		DefaultTab:                "orders",
		ShowAllTabs:               true,
		RequiresApproval:          false,
		RequiresApprovalGuestList: true,
		RequiresApprovalVIPList:   false,
		GenderBasedPricing:        false,
		ServiceFeePercent:         0,
		BottleService:             false,
		PaymentDeadlineHours:      48,
		AllowGuestSelfPayment:     true,
		AllowHostPayAll:           true,
		MinGuestsPerVIPList:       1,
		MaxGuestsPerVIPList:       50,
		GuestListAutoApprove:      false,
		GuestListCapacityLimit:    0, // 0 = unlimited
		QRCodeRequired:            true,
		AllowWalkIns:              true,
		AllowTransfers:            false,
		AllowResale:               false,
		MultiScanAllowed:          false,
		CheckInWindowHours:        4,
		SendWhatsApp:              true,
		SendEmail:                 true,
		SendPush:                  true,
	}
}

// VIPListFeatures returns features optimized for VIP list venues
func VIPListFeatures() VenueFeatures {
	return VenueFeatures{
		FlowType:                  FlowTypeVIPList,
		EnabledModules:            []string{"vip_lists", "bottle_service"},
		DefaultTab:                "vip_lists",
		ShowAllTabs:               false,
		RequiresApproval:          false,
		RequiresApprovalGuestList: true,
		RequiresApprovalVIPList:   false,
		GenderBasedPricing:        true,
		ServiceFeePercent:         15,
		BottleService:             true,
		PaymentDeadlineHours:      48,
		AllowGuestSelfPayment:     true,
		AllowHostPayAll:           true,
		MinGuestsPerVIPList:       2,
		MaxGuestsPerVIPList:       30,
		GuestListAutoApprove:      false,
		GuestListCapacityLimit:    0,
		QRCodeRequired:            true,
		AllowWalkIns:              false,
		AllowTransfers:            false,
		AllowResale:               false,
		MultiScanAllowed:          false,
		CheckInWindowHours:        2,
		SendWhatsApp:              true,
		SendEmail:                 true,
		SendPush:                  true,
	}
}

// HasModule checks if a venue has a specific module enabled
func (f *VenueFeatures) HasModule(module string) bool {
	for _, m := range f.EnabledModules {
		if m == module {
			return true
		}
	}
	return false
}

// Venue represents a venue registered in the Pull platform
type Venue struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	Description    string     `json:"description,omitempty"`
	Image          string     `json:"image,omitempty"`
	CoverImage     string     `json:"cover_image,omitempty"`
	Gallery        []string   `json:"gallery,omitempty"`

	// Location
	Location  string   `json:"location"`
	Address   string   `json:"address,omitempty"`
	City      string   `json:"city"`
	Country   string   `json:"country"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`

	// Schedule
	OpenTime  string   `json:"open_time"`
	CloseTime string   `json:"close_time"`
	Days      []string `json:"days"`
	Timezone  string   `json:"timezone"`

	// Contact (column names: contact_email, contact_phone)
	ContactEmail   string `json:"contact_email,omitempty"`
	ContactPhone   string `json:"contact_phone,omitempty"`
	WhatsappNumber string `json:"whatsapp_number,omitempty"`

	// Settings
	Currency string `json:"currency"`
	MinAge   int    `json:"min_age"`
	Capacity *int   `json:"capacity,omitempty"`

	// =============================================
	// ENHANCED FEATURE FLAGS SYSTEM
	// =============================================

	// Primary feature configuration (stored as JSONB in DB)
	Features VenueFeatures `json:"features"`

	// Legacy flags (kept for backwards compatibility, mapped from Features)
	UseVipListFlow           bool `json:"use_vip_list_flow"`
	UseGuestListFlow         bool `json:"use_guest_list_flow"`
	UseIndividualTickets     bool `json:"use_individual_tickets"`
	UseGroupReservations     bool `json:"use_group_reservations"`
	RequireApprovalOrders    bool `json:"require_approval_orders"`
	RequireApprovalGuestList bool `json:"require_approval_guest_list"`

	// Payment
	PaymentGateway       string                 `json:"payment_gateway"`
	PaymentGatewayConfig map[string]interface{} `json:"payment_gateway_config,omitempty"`

	// Platform fees
	PlatformFeePercent float64 `json:"platform_fee_percent"`
	PlatformFeeFixed   float64 `json:"platform_fee_fixed"`

	// Extra
	SocialLinks map[string]string      `json:"social_links,omitempty"`
	Settings    map[string]interface{} `json:"settings,omitempty"`

	// Status
	IsActive  bool       `json:"is_active"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// SyncLegacyFlags updates legacy boolean flags from Features struct
// Call this after loading venue from DB to ensure backwards compatibility
func (v *Venue) SyncLegacyFlags() {
	v.UseVipListFlow = v.Features.FlowType == FlowTypeVIPList || v.Features.HasModule("vip_lists")
	v.UseGuestListFlow = v.Features.FlowType == FlowTypeGuestList || v.Features.HasModule("guest_lists")
	v.UseIndividualTickets = v.Features.HasModule("individual_tickets")
	v.UseGroupReservations = v.Features.HasModule("group_reservations")
	v.RequireApprovalOrders = v.Features.RequiresApproval
	v.RequireApprovalGuestList = v.Features.RequiresApprovalGuestList
}

// GetMobileAppConfig returns configuration optimized for the mobile app
func (v *Venue) GetMobileAppConfig() map[string]interface{} {
	return map[string]interface{}{
		// Basic info
		"id":              v.ID,
		"organization_id": v.OrganizationID,
		"name":            v.Name,
		"slug":            v.Slug,
		"image":           v.Image,
		"currency":        v.Currency,
		"timezone":        v.Timezone,
		"payment_gateway": v.PaymentGateway,

		// Flow configuration (what the mobile app needs)
		"flow_type":       v.Features.FlowType,
		"enabled_modules": v.Features.EnabledModules,
		"default_tab":     v.Features.DefaultTab,
		"show_all_tabs":   v.Features.ShowAllTabs,

		// Legacy flags (for backwards compatibility)
		"use_vip_list_flow":           v.UseVipListFlow,
		"use_guest_list_flow":         v.UseGuestListFlow,
		"use_individual_tickets":      v.UseIndividualTickets,
		"use_group_reservations":      v.UseGroupReservations,
		"require_approval_orders":     v.RequireApprovalOrders,
		"require_approval_guest_list": v.RequireApprovalGuestList,

		// VIP List specific config
		"gender_based_pricing":      v.Features.GenderBasedPricing,
		"service_fee_percent":       v.Features.ServiceFeePercent,
		"bottle_service":            v.Features.BottleService,
		"payment_deadline_hours":    v.Features.PaymentDeadlineHours,
		"allow_guest_self_payment":  v.Features.AllowGuestSelfPayment,
		"allow_host_pay_all":        v.Features.AllowHostPayAll,
		"min_guests_per_vip_list":   v.Features.MinGuestsPerVIPList,
		"max_guests_per_vip_list":   v.Features.MaxGuestsPerVIPList,

		// Notification preferences
		"send_whatsapp": v.Features.SendWhatsApp,
		"send_email":    v.Features.SendEmail,
		"send_push":     v.Features.SendPush,
	}
}

// =============================================
// VENUE DATABASE CONFIG (Central DB)
// Matches pull-central.venue_database_configs table
// =============================================

// VenueDatabaseConfig stores database connection info for each venue
type VenueDatabaseConfig struct {
	ID          string `json:"id"`
	VenueID     string `json:"venue_id"`
	SupabaseURL string `json:"supabase_url"`
	ServiceKey  string `json:"-"` // Never expose in JSON
	AnonKey     string `json:"-"` // Never expose in JSON
	IsActive    bool   `json:"is_active"`

	// Migration tracking
	MigrationStatus    string     `json:"migration_status"` // pending, in_progress, completed, failed
	MigrationStartedAt *time.Time `json:"migration_started_at,omitempty"`
	MigratedAt         *time.Time `json:"migrated_at,omitempty"`
	LastSyncAt         *time.Time `json:"last_sync_at,omitempty"`
	RecordsMigrated    int        `json:"records_migrated"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy *string   `json:"created_by,omitempty"`
}

// =============================================
// ORGANIZATION (Central DB)
// Matches pull-central.organizations table
// =============================================

// Organization represents a company that owns venues
type Organization struct {
	ID            string                 `json:"id"`
	OwnerID       string                 `json:"owner_id"`
	Name          string                 `json:"name"`
	LegalName     string                 `json:"legal_name,omitempty"`
	TaxID         string                 `json:"tax_id,omitempty"`
	Email         string                 `json:"email,omitempty"`
	Phone         string                 `json:"phone,omitempty"`
	Address       string                 `json:"address,omitempty"`
	City          string                 `json:"city,omitempty"`
	Country       string                 `json:"country"`
	LogoURL       string                 `json:"logo_url,omitempty"`
	BillingConfig map[string]interface{} `json:"billing_config,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	DeletedAt     *time.Time             `json:"deleted_at,omitempty"`
}

// =============================================
// DATABASE CONNECTION (Runtime)
// =============================================

// DBConnection holds runtime connection info
type DBConnection struct {
	VenueID      string
	SupabaseURL  string
	ServiceKey   string
	AnonKey      string
	IsActive     bool
	LastHealthAt time.Time
	IsHealthy    bool
}
