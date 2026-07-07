package models

import "time"

// =============================================
// EVENT (Venue DB)
// =============================================

// Event represents an event at a venue
type Event struct {
	ID             string     `json:"id"`
	VenueID        string     `json:"venue_id"`
	OrganizationID string     `json:"organization_id"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	Description    string     `json:"description,omitempty"`
	Image          string     `json:"image,omitempty"`
	EventDate      string     `json:"event_date"`
	StartTime      string     `json:"start_time"`
	EndTime        string     `json:"end_time"`
	MinAge         int        `json:"min_age"`
	DressCode      string     `json:"dress_code,omitempty"`
	TicketLimit    int        `json:"ticket_limit"`
	Requirements   []string   `json:"requirements,omitempty"`
	IsPublished    bool       `json:"is_published"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

// CreateEventRequest for creating events
type CreateEventRequest struct {
	VenueID      string   `json:"venue_id" binding:"required"`
	Name         string   `json:"name" binding:"required"`
	Description  string   `json:"description"`
	Image        string   `json:"image"`
	EventDate    string   `json:"event_date" binding:"required"`
	StartTime    string   `json:"start_time" binding:"required"`
	EndTime      string   `json:"end_time" binding:"required"`
	MinAge       int      `json:"min_age"`
	DressCode    string   `json:"dress_code"`
	TicketLimit  int      `json:"ticket_limit"`
	Requirements []string `json:"requirements"`
}

// =============================================
// TICKET TYPE (Venue DB)
// =============================================

// TicketType represents a ticket category for an event
type TicketType struct {
	ID              string    `json:"id"`
	EventID         string    `json:"event_id"`
	Name            string    `json:"name"`
	Price           float64   `json:"price"`
	InitialQuantity int       `json:"initial_quantity"`
	CurrentQuantity int       `json:"current_quantity"`
	Benefits        string    `json:"benefits,omitempty"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateTicketTypeRequest for creating ticket types
type CreateTicketTypeRequest struct {
	EventID         string  `json:"event_id" binding:"required"`
	Name            string  `json:"name" binding:"required"`
	Price           float64 `json:"price" binding:"required"`
	InitialQuantity int     `json:"initial_quantity" binding:"required"`
	Benefits        string  `json:"benefits"`
}

// =============================================
// ORDER (Venue DB)
// =============================================

// Order represents a ticket purchase
type Order struct {
	ID                  string         `json:"id"`
	EventID             string         `json:"event_id"`
	TicketTypeID        string         `json:"ticket_type_id"`
	UserID              string         `json:"user_id"`
	VenueID             string         `json:"venue_id"`
	OrganizationID      string         `json:"organization_id"`
	Quantity            int            `json:"quantity"`
	UnitPrice           float64        `json:"unit_price"`
	Total               float64        `json:"total"`
	Status              string         `json:"status"` // pending, paid, cancelled, refunded
	PaymentLinkCode     string         `json:"payment_link_code"`
	PaymentGateway      PaymentGateway `json:"payment_gateway,omitempty"`

	// Stripe fields
	StripeSessionID     string         `json:"stripe_session_id,omitempty"`
	StripePaymentIntent string         `json:"stripe_payment_intent,omitempty"`

	// NeoNet fields
	NeoNetTransactionID string         `json:"neonet_transaction_id,omitempty"`
	NeoNetAuthCode      string         `json:"neonet_auth_code,omitempty"`

	// Approval workflow
	ApprovedBy          *string        `json:"approved_by,omitempty"`
	ApprovedAt          *time.Time     `json:"approved_at,omitempty"`
	RejectedBy          *string        `json:"rejected_by,omitempty"`
	RejectedAt          *time.Time     `json:"rejected_at,omitempty"`
	RejectReason        *string        `json:"reject_reason,omitempty"`

	// Timestamps
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	PaidAt              *time.Time     `json:"paid_at,omitempty"`
	ExpiresAt           *time.Time     `json:"expires_at,omitempty"`
}

// CreateOrderRequest for creating orders
type CreateOrderRequest struct {
	EventSlug    string `json:"event_slug" binding:"required"`
	TicketTypeID string `json:"ticket_type_id" binding:"required"`
	Quantity     int    `json:"quantity" binding:"required,min=1,max=5"`
	UserID       string `json:"user_id"`
}

// =============================================
// TICKET (Venue DB)
// =============================================

// Ticket represents an individual ticket
type Ticket struct {
	ID          string     `json:"id"`
	OrderID     string     `json:"order_id"`
	EventID     string     `json:"event_id"`
	TicketType  string     `json:"ticket_type"`
	OwnerName   string     `json:"owner_name"`
	OwnerEmail  string     `json:"owner_email"`
	QRToken     string     `json:"qr_token"`
	Status      string     `json:"status"` // valid, used, cancelled
	UsedAt      *time.Time `json:"used_at,omitempty"`
	UsedBy      *string    `json:"used_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// TicketOwner for ticket assignment
type TicketOwner struct {
	Name        string `json:"owner_name" binding:"required"`
	LastName    string `json:"owner_last_name"`
	Email       string `json:"owner_email" binding:"required,email"`
	Phone       string `json:"owner_phone"`
	PhonePrefix string `json:"owner_phone_prefix"`
	BirthDate   string `json:"owner_birth_date"`
	Gender      string `json:"owner_gender"`
}
