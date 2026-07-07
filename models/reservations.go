package models

import "time"

// =============================================
// GROUP RESERVATIONS (VIP Tables) - Venue DB
// =============================================

// ReservationStatus represents the status of a reservation
type ReservationStatus string

const (
	ReservationStatusPending   ReservationStatus = "pending"
	ReservationStatusConfirmed ReservationStatus = "confirmed"
	ReservationStatusPaid      ReservationStatus = "paid"
	ReservationStatusCancelled ReservationStatus = "cancelled"
	ReservationStatusCompleted ReservationStatus = "completed"
	ReservationStatusNoShow    ReservationStatus = "no_show"
)

// GroupReservation represents a VIP table/group reservation
type GroupReservation struct {
	ID              string            `json:"id"`
	EventID         string            `json:"event_id"`
	VenueID         string            `json:"venue_id"`
	OrganizationID  string            `json:"organization_id"`
	OrganizerUserID string            `json:"organizer_user_id"`
	TableNumber     *string           `json:"table_number,omitempty"`
	TableZone       *string           `json:"table_zone,omitempty"`
	Status          ReservationStatus `json:"status"`

	// Capacity
	MinGuests    int `json:"min_guests"`
	MaxGuests    int `json:"max_guests"`
	CurrentGuests int `json:"current_guests"`

	// Pricing
	MinimumSpend   float64 `json:"minimum_spend"`
	DepositAmount  float64 `json:"deposit_amount"`
	DepositPaid    bool    `json:"deposit_paid"`
	TotalSpent     float64 `json:"total_spent"`

	// Payment
	PaymentGateway       PaymentGateway `json:"payment_gateway,omitempty"`
	PaymentLinkCode      string         `json:"payment_link_code,omitempty"`
	StripeSessionID      *string        `json:"stripe_session_id,omitempty"`
	StripePaymentIntent  *string        `json:"stripe_payment_intent,omitempty"`

	// Contact
	OrganizerName  string `json:"organizer_name"`
	OrganizerEmail string `json:"organizer_email"`
	OrganizerPhone string `json:"organizer_phone,omitempty"`

	// QR for group check-in
	QRToken string `json:"qr_token,omitempty"`

	// Notes
	SpecialRequests *string `json:"special_requests,omitempty"`
	InternalNotes   *string `json:"internal_notes,omitempty"`

	// Approval workflow
	ApprovedBy   *string    `json:"approved_by,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	RejectedBy   *string    `json:"rejected_by,omitempty"`
	RejectedAt   *time.Time `json:"rejected_at,omitempty"`
	RejectReason *string    `json:"reject_reason,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`
	CheckedInAt *time.Time `json:"checked_in_at,omitempty"`

	// Nested data (populated on fetch)
	Event    *Event                    `json:"event,omitempty"`
	Guests   []GroupReservationGuest   `json:"guests,omitempty"`
	Bottles  []GroupReservationBottle  `json:"bottles,omitempty"`
}

// GroupReservationGuest represents a guest in a group reservation
type GroupReservationGuest struct {
	ID                 string            `json:"id"`
	GroupReservationID string            `json:"group_reservation_id"`
	UserID             *string           `json:"user_id,omitempty"`
	Name               string            `json:"name"`
	Email              string            `json:"email,omitempty"`
	Phone              string            `json:"phone,omitempty"`
	Gender             string            `json:"gender,omitempty"`
	IsOrganizer        bool              `json:"is_organizer"`
	Status             ReservationStatus `json:"status"`
	QRToken            string            `json:"qr_token,omitempty"`

	// Payment (if guest pays separately)
	AmountToPay    float64        `json:"amount_to_pay"`
	AmountPaid     float64        `json:"amount_paid"`
	PaymentGateway PaymentGateway `json:"payment_gateway,omitempty"`
	PaymentLinkCode string        `json:"payment_link_code,omitempty"`

	// Check-in
	CheckedIn   bool       `json:"checked_in"`
	CheckedInAt *time.Time `json:"checked_in_at,omitempty"`
	CheckedInBy *string    `json:"checked_in_by,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	InvitedAt   *time.Time `json:"invited_at,omitempty"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

// GroupReservationBottle represents a bottle in a group reservation
type GroupReservationBottle struct {
	ID                 string    `json:"id"`
	GroupReservationID string    `json:"group_reservation_id"`
	BottleID           string    `json:"bottle_id"`
	BottleName         string    `json:"bottle_name"`
	BottleCategory     string    `json:"bottle_category"`
	Quantity           int       `json:"quantity"`
	UnitPrice          float64   `json:"unit_price"`
	TotalPrice         float64   `json:"total_price"`
	MixerID            *string   `json:"mixer_id,omitempty"`
	MixerName          *string   `json:"mixer_name,omitempty"`
	MixerQuantity      int       `json:"mixer_quantity"`
	MixerPrice         float64   `json:"mixer_price"`
	CreatedAt          time.Time `json:"created_at"`
}

// CreateGroupReservationRequest for creating reservations
type CreateGroupReservationRequest struct {
	EventID         string                          `json:"event_id" binding:"required"`
	OrganizerName   string                          `json:"organizer_name" binding:"required"`
	OrganizerEmail  string                          `json:"organizer_email" binding:"required,email"`
	OrganizerPhone  string                          `json:"organizer_phone"`
	MaxGuests       int                             `json:"max_guests" binding:"required,min=1,max=50"`
	TableZone       string                          `json:"table_zone"`
	SpecialRequests string                          `json:"special_requests"`
	Bottles         []CreateReservationBottleInput  `json:"bottles"`
	Guests          []CreateReservationGuestInput   `json:"guests"`
}

// CreateReservationBottleInput for adding bottles
type CreateReservationBottleInput struct {
	BottleID      string  `json:"bottle_id" binding:"required"`
	Quantity      int     `json:"quantity" binding:"required,min=1"`
	MixerID       *string `json:"mixer_id"`
	MixerQuantity int     `json:"mixer_quantity"`
}

// CreateReservationGuestInput for adding guests
type CreateReservationGuestInput struct {
	Name   string `json:"name" binding:"required"`
	Email  string `json:"email" binding:"email"`
	Phone  string `json:"phone"`
	Gender string `json:"gender"`
}

// =============================================
// VIP BOTTLES CATALOG (Venue DB)
// =============================================

// VIPBottle represents a bottle in the venue's catalog
type VIPBottle struct {
	ID           string    `json:"id"`
	VenueID      string    `json:"venue_id"`
	Name         string    `json:"name"`
	Brand        string    `json:"brand,omitempty"`
	Category     string    `json:"category"` // vodka, whiskey, champagne, tequila, rum, gin, etc.
	Size         string    `json:"size"`     // 750ml, 1L, 1.5L, etc.
	BasePrice    float64   `json:"base_price"`
	Description  string    `json:"description,omitempty"`
	ImageURL     string    `json:"image_url,omitempty"`
	IsActive     bool      `json:"is_active"`
	DisplayOrder int       `json:"display_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// VIPMixer represents a mixer option
type VIPMixer struct {
	ID           string    `json:"id"`
	VenueID      string    `json:"venue_id"`
	Name         string    `json:"name"`
	Price        float64   `json:"price"`
	IsActive     bool      `json:"is_active"`
	DisplayOrder int       `json:"display_order"`
	CreatedAt    time.Time `json:"created_at"`
}

// =============================================
// VIP LIST RESERVATIONS (Venue DB)
// =============================================

// VIPListReservation represents a VIP list entry (organizer creates list, invites guests)
type VIPListReservation struct {
	ID              string            `json:"id"`
	EventID         string            `json:"event_id"`
	VenueID         string            `json:"venue_id"`
	OrganizationID  string            `json:"organization_id"`
	OrganizerUserID string            `json:"organizer_user_id"`
	ListName        string            `json:"list_name"`
	Status          ReservationStatus `json:"status"`

	// Capacity
	MaxGuests     int `json:"max_guests"`
	CurrentGuests int `json:"current_guests"`

	// Pricing
	PricePerPerson   float64 `json:"price_per_person"`
	OrganizerPays    bool    `json:"organizer_pays"` // If true, organizer pays for all
	TotalAmount      float64 `json:"total_amount"`
	AmountPaid       float64 `json:"amount_paid"`

	// Organizer info
	OrganizerName  string `json:"organizer_name"`
	OrganizerEmail string `json:"organizer_email"`
	OrganizerPhone string `json:"organizer_phone,omitempty"`

	// Payment
	PaymentGateway      PaymentGateway `json:"payment_gateway,omitempty"`
	PaymentLinkCode     string         `json:"payment_link_code,omitempty"`
	StripeSessionID     *string        `json:"stripe_session_id,omitempty"`
	StripePaymentIntent *string        `json:"stripe_payment_intent,omitempty"`

	// QR Token for organizer
	QRToken string `json:"qr_token,omitempty"`

	// Notes
	SpecialRequests *string `json:"special_requests,omitempty"`
	InternalNotes   *string `json:"internal_notes,omitempty"`

	// Approval
	RequiresApproval bool       `json:"requires_approval"`
	ApprovedBy       *string    `json:"approved_by,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
	RejectedBy       *string    `json:"rejected_by,omitempty"`
	RejectedAt       *time.Time `json:"rejected_at,omitempty"`
	RejectReason     *string    `json:"reject_reason,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`

	// Nested
	Event  *Event         `json:"event,omitempty"`
	Guests []VIPListGuest `json:"guests,omitempty"`
}

// VIPListGuest represents a guest on a VIP list
type VIPListGuest struct {
	ID                   string            `json:"id"`
	VIPListReservationID string            `json:"vip_list_reservation_id"`
	UserID               *string           `json:"user_id,omitempty"`
	Name                 string            `json:"name"`
	Email                string            `json:"email,omitempty"`
	Phone                string            `json:"phone,omitempty"`
	Gender               string            `json:"gender,omitempty"`
	IsOrganizer          bool              `json:"is_organizer"`
	Status               ReservationStatus `json:"status"`
	QRToken              string            `json:"qr_token,omitempty"`

	// Payment (if guest pays individually)
	AmountToPay     float64        `json:"amount_to_pay"`
	AmountPaid      float64        `json:"amount_paid"`
	PaymentGateway  PaymentGateway `json:"payment_gateway,omitempty"`
	PaymentLinkCode string         `json:"payment_link_code,omitempty"`

	// Check-in
	CheckedIn   bool       `json:"checked_in"`
	CheckedInAt *time.Time `json:"checked_in_at,omitempty"`
	CheckedInBy *string    `json:"checked_in_by,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	InvitedAt   *time.Time `json:"invited_at,omitempty"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

// CreateVIPListRequest for creating a VIP list
type CreateVIPListRequest struct {
	EventID         string                      `json:"event_id" binding:"required"`
	ListName        string                      `json:"list_name" binding:"required"`
	MaxGuests       int                         `json:"max_guests" binding:"required,min=1,max=100"`
	OrganizerName   string                      `json:"organizer_name" binding:"required"`
	OrganizerEmail  string                      `json:"organizer_email" binding:"required,email"`
	OrganizerPhone  string                      `json:"organizer_phone"`
	OrganizerPays   bool                        `json:"organizer_pays"`
	SpecialRequests string                      `json:"special_requests"`
	Guests          []CreateVIPListGuestInput   `json:"guests"`
}

// CreateVIPListGuestInput for adding guests to a VIP list
type CreateVIPListGuestInput struct {
	Name   string `json:"name" binding:"required"`
	Email  string `json:"email" binding:"email"`
	Phone  string `json:"phone"`
	Gender string `json:"gender"`
}

// =============================================
// GUEST LIST (Free entry lists) - Venue DB
// =============================================

// GuestListType represents a type of guest list for an event
type GuestListType struct {
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	VenueID          string    `json:"venue_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	MaxCapacity      int       `json:"max_capacity"`
	CurrentCount     int       `json:"current_count"`
	EntryTime        *string   `json:"entry_time,omitempty"`      // e.g., "Before 11pm"
	EntryBenefit     string    `json:"entry_benefit,omitempty"`   // e.g., "Free entry", "50% off"
	RequiresApproval bool      `json:"requires_approval"`
	Gender           *string   `json:"gender,omitempty"`          // male, female, any
	IsActive         bool      `json:"is_active"`
	ClosesAt         *string   `json:"closes_at,omitempty"`       // Time when signups close
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GuestListSignup represents a signup for a guest list
type GuestListSignup struct {
	ID              string    `json:"id"`
	GuestListTypeID string    `json:"guest_list_type_id"`
	EventID         string    `json:"event_id"`
	VenueID         string    `json:"venue_id"`
	UserID          *string   `json:"user_id,omitempty"`
	Name            string    `json:"name"`
	Surname         string    `json:"surname,omitempty"`
	Email           string    `json:"email"`
	Phone           string    `json:"phone,omitempty"`
	Gender          string    `json:"gender,omitempty"`
	BirthDate       *string   `json:"birth_date,omitempty"`
	PlusOnes        int       `json:"plus_ones"`
	Status          string    `json:"status"` // pending, approved, rejected, checked_in, no_show
	QRToken         string    `json:"qr_token,omitempty"`

	// Approval
	ApprovedBy   *string    `json:"approved_by,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	RejectedBy   *string    `json:"rejected_by,omitempty"`
	RejectedAt   *time.Time `json:"rejected_at,omitempty"`
	RejectReason *string    `json:"reject_reason,omitempty"`

	// Check-in
	CheckedIn      bool       `json:"checked_in"`
	CheckedInAt    *time.Time `json:"checked_in_at,omitempty"`
	CheckedInBy    *string    `json:"checked_in_by,omitempty"`
	PlusOnesUsed   int        `json:"plus_ones_used"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Nested
	GuestListType *GuestListType `json:"guest_list_type,omitempty"`
}

// GuestListSignupRequest for signing up to a guest list
type GuestListSignupRequest struct {
	GuestListTypeID string `json:"guest_list_type_id" binding:"required"`
	EventID         string `json:"event_id" binding:"required"`
	Name            string `json:"name" binding:"required"`
	Surname         string `json:"surname"`
	Email           string `json:"email" binding:"required,email"`
	Phone           string `json:"phone"`
	Gender          string `json:"gender"`
	BirthDate       string `json:"birth_date"`
	PlusOnes        int    `json:"plus_ones" binding:"min=0,max=5"`
}

// =============================================
// RESPONSE TYPES
// =============================================

// GroupReservationResponse with full data
type GroupReservationResponse struct {
	GroupReservation
	TotalBottlesCost float64 `json:"total_bottles_cost"`
	GuestsPaid       int     `json:"guests_paid"`
	GuestsCheckedIn  int     `json:"guests_checked_in"`
}

// VIPListResponse with stats
type VIPListResponse struct {
	VIPListReservation
	GuestsConfirmed int `json:"guests_confirmed"`
	GuestsPaid      int `json:"guests_paid"`
	GuestsCheckedIn int `json:"guests_checked_in"`
}

// EventGuestListSummary for event overview
type EventGuestListSummary struct {
	EventID            string `json:"event_id"`
	TotalGuestLists    int    `json:"total_guest_lists"`
	TotalSignups       int    `json:"total_signups"`
	ApprovedSignups    int    `json:"approved_signups"`
	PendingSignups     int    `json:"pending_signups"`
	CheckedInSignups   int    `json:"checked_in_signups"`
}
