package models

import "time"

// =============================================
// NOTIFICATIONS (Venue DB)
// =============================================

// NotificationType represents the type of notification
type NotificationType string

const (
	// Orders
	NotifyNewOrder           NotificationType = "new_order"
	NotifyOrderPaid          NotificationType = "order_paid"
	NotifyOrderCancelled     NotificationType = "order_cancelled"
	NotifyOrderExpired       NotificationType = "order_expired"

	// Reservations
	NotifyNewReservation     NotificationType = "new_reservation"
	NotifyReservationPaid    NotificationType = "reservation_paid"
	NotifyReservationUpdate  NotificationType = "reservation_update"
	NotifyReservationCancel  NotificationType = "reservation_cancelled"
	NotifyGuestJoined        NotificationType = "guest_joined"
	NotifyGuestLeft          NotificationType = "guest_left"

	// VIP List
	NotifyNewVIPList         NotificationType = "new_vip_list"
	NotifyVIPListPaid        NotificationType = "vip_list_paid"
	NotifyVIPListGuestJoined NotificationType = "vip_list_guest_joined"

	// Guest List
	NotifyGuestListSignup    NotificationType = "guest_list_signup"
	NotifyGuestListApproved  NotificationType = "guest_list_approved"
	NotifyGuestListRejected  NotificationType = "guest_list_rejected"

	// Check-in
	NotifyTicketCheckedIn    NotificationType = "ticket_checked_in"
	NotifyGuestCheckedIn     NotificationType = "guest_checked_in"

	// Events
	NotifyEventCreated       NotificationType = "event_created"
	NotifyEventUpdated       NotificationType = "event_updated"
	NotifyEventSoldOut       NotificationType = "event_sold_out"

	// System
	NotifySystemAlert        NotificationType = "system_alert"
	NotifyPaymentFailed      NotificationType = "payment_failed"
)

// NotificationPriority represents notification priority level
type NotificationPriority string

const (
	PriorityLow    NotificationPriority = "low"
	PriorityNormal NotificationPriority = "normal"
	PriorityHigh   NotificationPriority = "high"
	PriorityUrgent NotificationPriority = "urgent"
)

// NotificationChannel represents delivery channel
type NotificationChannel string

const (
	ChannelInApp NotificationChannel = "in_app"
	ChannelPush  NotificationChannel = "push"
	ChannelEmail NotificationChannel = "email"
	ChannelSMS   NotificationChannel = "sms"
)

// Notification represents a notification in the system
type Notification struct {
	ID             string               `json:"id"`
	VenueID        string               `json:"venue_id"`
	OrganizationID string               `json:"organization_id"`
	Type           NotificationType     `json:"type"`
	Priority       NotificationPriority `json:"priority"`
	Title          string               `json:"title"`
	Message        string               `json:"message"`
	Data           map[string]interface{} `json:"data,omitempty"`

	// Target
	TargetType string  `json:"target_type"` // staff, user
	TargetID   *string `json:"target_id,omitempty"`
	TargetRole *string `json:"target_role,omitempty"` // For staff: admin, manager, etc.

	// References
	EventID            *string `json:"event_id,omitempty"`
	OrderID            *string `json:"order_id,omitempty"`
	TicketID           *string `json:"ticket_id,omitempty"`
	GroupReservationID *string `json:"group_reservation_id,omitempty"`
	VIPListID          *string `json:"vip_list_id,omitempty"`
	GuestListSignupID  *string `json:"guest_list_signup_id,omitempty"`
	UserID             *string `json:"user_id,omitempty"`

	// Delivery
	Channels    []NotificationChannel `json:"channels"`
	SentVia     []NotificationChannel `json:"sent_via,omitempty"`
	DeliveredAt *time.Time            `json:"delivered_at,omitempty"`

	// Status
	IsRead     bool       `json:"is_read"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	IsArchived bool       `json:"is_archived"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`

	// Action
	ActionURL  *string `json:"action_url,omitempty"`
	ActionText *string `json:"action_text,omitempty"`

	// Timestamps
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// CreateNotificationInput for creating notifications
type CreateNotificationInput struct {
	VenueID        string
	OrganizationID string
	Type           NotificationType
	Priority       NotificationPriority
	Title          string
	Message        string
	Data           map[string]interface{}
	TargetType     string
	TargetID       *string
	TargetRole     *string
	EventID        *string
	OrderID        *string
	TicketID       *string
	ReservationID  *string
	UserID         *string
	Channels       []NotificationChannel
	ActionURL      *string
	ActionText     *string
}

// NotificationPreferences represents user/staff notification preferences
type NotificationPreferences struct {
	ID           string `json:"id"`
	TargetType   string `json:"target_type"` // staff, user
	TargetID     string `json:"target_id"`
	VenueID      string `json:"venue_id"`

	// Channel preferences
	InAppEnabled  bool `json:"in_app_enabled"`
	PushEnabled   bool `json:"push_enabled"`
	EmailEnabled  bool `json:"email_enabled"`
	SMSEnabled    bool `json:"sms_enabled"`

	// Type preferences (which notifications to receive)
	EnabledTypes []NotificationType `json:"enabled_types,omitempty"`

	// Quiet hours
	QuietHoursStart *string `json:"quiet_hours_start,omitempty"` // HH:MM
	QuietHoursEnd   *string `json:"quiet_hours_end,omitempty"`   // HH:MM

	// Device tokens
	FCMToken  *string `json:"fcm_token,omitempty"`
	APNSToken *string `json:"apns_token,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PushToken for device registration
type PushToken struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	DeviceID   string    `json:"device_id"`
	Token      string    `json:"token"`
	Platform   string    `json:"platform"` // ios, android, web
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// RegisterPushTokenRequest for registering push tokens
type RegisterPushTokenRequest struct {
	DeviceID string `json:"device_id" binding:"required"`
	Token    string `json:"token" binding:"required"`
	Platform string `json:"platform" binding:"required,oneof=ios android web"`
}

// NotificationResponse with pagination
type NotificationListResponse struct {
	Notifications []Notification `json:"notifications"`
	Total         int            `json:"total"`
	UnreadCount   int            `json:"unread_count"`
	Page          int            `json:"page"`
	Limit         int            `json:"limit"`
}

// =============================================
// NOTIFICATION TEMPLATES
// =============================================

// NotificationTemplate stores reusable notification templates
type NotificationTemplate struct {
	ID        string           `json:"id"`
	VenueID   *string          `json:"venue_id,omitempty"` // nil = system template
	Type      NotificationType `json:"type"`
	Language  string           `json:"language"` // es, en
	Title     string           `json:"title"`    // Supports {{variables}}
	Message   string           `json:"message"`  // Supports {{variables}}
	IsActive  bool             `json:"is_active"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// Default notification templates
var DefaultNotificationTemplates = map[NotificationType]map[string]NotificationTemplate{
	NotifyNewOrder: {
		"es": {Type: NotifyNewOrder, Language: "es", Title: "Nueva orden recibida", Message: "{{user_name}} ha realizado una orden de {{quantity}} entradas para {{event_name}}"},
		"en": {Type: NotifyNewOrder, Language: "en", Title: "New order received", Message: "{{user_name}} placed an order for {{quantity}} tickets to {{event_name}}"},
	},
	NotifyOrderPaid: {
		"es": {Type: NotifyOrderPaid, Language: "es", Title: "Pago confirmado", Message: "El pago de {{user_name}} por {{amount}} ha sido confirmado"},
		"en": {Type: NotifyOrderPaid, Language: "en", Title: "Payment confirmed", Message: "Payment of {{amount}} from {{user_name}} has been confirmed"},
	},
	NotifyNewReservation: {
		"es": {Type: NotifyNewReservation, Language: "es", Title: "Nueva reservación VIP", Message: "{{organizer_name}} ha solicitado una reservación para {{guest_count}} personas"},
		"en": {Type: NotifyNewReservation, Language: "en", Title: "New VIP reservation", Message: "{{organizer_name}} has requested a reservation for {{guest_count}} guests"},
	},
	NotifyGuestListSignup: {
		"es": {Type: NotifyGuestListSignup, Language: "es", Title: "Nueva inscripción", Message: "{{user_name}} se ha inscrito en la lista {{list_name}}"},
		"en": {Type: NotifyGuestListSignup, Language: "en", Title: "New signup", Message: "{{user_name}} has signed up for {{list_name}}"},
	},
	NotifyTicketCheckedIn: {
		"es": {Type: NotifyTicketCheckedIn, Language: "es", Title: "Check-in realizado", Message: "{{user_name}} ha ingresado al evento"},
		"en": {Type: NotifyTicketCheckedIn, Language: "en", Title: "Check-in completed", Message: "{{user_name}} has checked into the event"},
	},
}
