package controllers

import (
	"fmt"
	"html"
	"log"
	"math"
	"regexp"
	"strings"
	"time"

	"pull-api-v2/config"
	"pull-api-v2/services"
)

// =============================================
// EVENT VALIDATION CONSTANTS
// =============================================

const (
	// Event limits
	MaxEventNameLength        = 255
	MaxEventDescriptionLength = 5000
	MaxEventLocationLength    = 500
	MaxEventDressCodeLength   = 500
	MaxTicketLimit            = 100000
	MaxTableCapacity          = 10000
	MaxMinAge                 = 120
	MinEventDurationMinutes   = 15
	MaxEventDurationHours     = 24
	MaxFutureEventYears       = 2

	// Ticket type limits
	MaxTicketTypeName        = 100
	MaxTicketTypeDescription = 1000
	MaxTicketPrice           = 999999.99
	MinTicketPrice           = 0.01
	MaxTicketQuantity        = 50000
	MaxTicketPerOrder        = 100
)

// Slug validation pattern
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// =============================================
// EVENT VALIDATION FUNCTIONS
// =============================================

// ValidateEventDateTime validates event date and time fields
// Returns error if validation fails
func ValidateEventDateTime(eventDate, startTime, endTime string) error {
	// Parse date
	parsedDate, err := time.Parse("2006-01-02", eventDate)
	if err != nil {
		return fmt.Errorf("invalid event date format, expected YYYY-MM-DD")
	}

	// Validate not in past (allow 1 day grace for timezone differences)
	yesterday := time.Now().AddDate(0, 0, -1)
	if parsedDate.Before(yesterday) {
		return fmt.Errorf("event date cannot be in the past")
	}

	// Validate not too far in future
	maxDate := time.Now().AddDate(MaxFutureEventYears, 0, 0)
	if parsedDate.After(maxDate) {
		return fmt.Errorf("event date cannot be more than %d years in the future", MaxFutureEventYears)
	}

	// Parse times
	startT, err := parseTimeFlexible(startTime)
	if err != nil {
		return fmt.Errorf("invalid start time format")
	}

	endT, err := parseTimeFlexible(endTime)
	if err != nil {
		return fmt.Errorf("invalid end time format")
	}

	// Validate start < end (unless event spans midnight)
	// For events that span midnight, end time could be less than start
	duration := endT.Sub(startT)
	if duration < 0 {
		// Event spans midnight, add 24 hours
		duration += 24 * time.Hour
	}

	// Minimum duration check
	if duration < MinEventDurationMinutes*time.Minute {
		return fmt.Errorf("event must be at least %d minutes long", MinEventDurationMinutes)
	}

	// Maximum duration check
	if duration > MaxEventDurationHours*time.Hour {
		return fmt.Errorf("event cannot exceed %d hours", MaxEventDurationHours)
	}

	return nil
}

// parseTimeFlexible parses time in HH:MM or HH:MM:SS format
func parseTimeFlexible(timeStr string) (time.Time, error) {
	// Try HH:MM:SS first
	t, err := time.Parse("15:04:05", timeStr)
	if err == nil {
		return t, nil
	}

	// Try HH:MM
	t, err = time.Parse("15:04", timeStr)
	if err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("invalid time format")
}

// ValidateEventLimits validates numeric event fields
func ValidateEventLimits(ticketLimit, tableCapacity, minAge int) error {
	// Validate ticket limit
	if ticketLimit < 0 {
		return fmt.Errorf("ticket_limit must be non-negative")
	}
	if ticketLimit > MaxTicketLimit {
		return fmt.Errorf("ticket_limit exceeds maximum of %d", MaxTicketLimit)
	}

	// Validate table capacity
	if tableCapacity < 0 {
		return fmt.Errorf("table_capacity must be non-negative")
	}
	if tableCapacity > MaxTableCapacity {
		return fmt.Errorf("table_capacity exceeds maximum of %d", MaxTableCapacity)
	}

	// Validate logical relationship
	if ticketLimit > 0 && tableCapacity > 0 {
		if tableCapacity > ticketLimit {
			return fmt.Errorf("table_capacity cannot exceed ticket_limit")
		}
	}

	// Validate min age
	if minAge < 0 {
		return fmt.Errorf("min_age must be non-negative")
	}
	if minAge > MaxMinAge {
		return fmt.Errorf("min_age cannot exceed %d", MaxMinAge)
	}

	return nil
}

// ValidateSlug validates slug format
func ValidateSlug(slug string) error {
	if slug == "" {
		return nil // Empty is allowed, will be generated
	}

	if len(slug) < 2 {
		return fmt.Errorf("slug must be at least 2 characters")
	}

	if len(slug) > 255 {
		return fmt.Errorf("slug cannot exceed 255 characters")
	}

	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must contain only lowercase letters, numbers, and hyphens")
	}

	return nil
}

// =============================================
// TICKET TYPE VALIDATION FUNCTIONS
// =============================================

// ValidateTicketPricing validates ticket type pricing
func ValidateTicketPricing(price, malePrice, femalePrice float64, hasGenderPricing bool) error {
	// Validate main price
	if price < 0 {
		return fmt.Errorf("price cannot be negative")
	}
	if price > MaxTicketPrice {
		return fmt.Errorf("price exceeds maximum of %.2f", MaxTicketPrice)
	}

	// Validate decimal precision (2 places max)
	if !hasValidPrecision(price) {
		return fmt.Errorf("price must have at most 2 decimal places")
	}

	// If gender pricing enabled, validate those prices
	if hasGenderPricing {
		if malePrice < 0 || femalePrice < 0 {
			return fmt.Errorf("gender prices cannot be negative")
		}
		if malePrice > MaxTicketPrice || femalePrice > MaxTicketPrice {
			return fmt.Errorf("gender prices exceed maximum of %.2f", MaxTicketPrice)
		}

		// At least one gender price should be set
		if malePrice == 0 && femalePrice == 0 {
			return fmt.Errorf("at least one gender price must be set when gender pricing is enabled")
		}

		// Validate precision
		if !hasValidPrecision(malePrice) || !hasValidPrecision(femalePrice) {
			return fmt.Errorf("gender prices must have at most 2 decimal places")
		}
	}

	return nil
}

// ValidateTicketQuantity validates ticket quantity limits
func ValidateTicketQuantity(initialQty, minQty, maxQty int) error {
	// Validate initial quantity
	if initialQty < 1 {
		return fmt.Errorf("initial_quantity must be at least 1")
	}
	if initialQty > MaxTicketQuantity {
		return fmt.Errorf("initial_quantity exceeds maximum of %d", MaxTicketQuantity)
	}

	// Validate min quantity
	if minQty < 1 {
		return fmt.Errorf("min_quantity must be at least 1")
	}
	if minQty > MaxTicketPerOrder {
		return fmt.Errorf("min_quantity exceeds maximum of %d", MaxTicketPerOrder)
	}

	// Validate max quantity
	if maxQty < 1 {
		return fmt.Errorf("max_quantity must be at least 1")
	}
	if maxQty > MaxTicketPerOrder {
		return fmt.Errorf("max_quantity exceeds maximum of %d", MaxTicketPerOrder)
	}

	// Validate relationships
	if maxQty < minQty {
		return fmt.Errorf("max_quantity cannot be less than min_quantity")
	}

	if maxQty > initialQty {
		return fmt.Errorf("max_quantity cannot exceed initial_quantity")
	}

	return nil
}

// hasValidPrecision checks if price has at most 2 decimal places
func hasValidPrecision(price float64) bool {
	// Multiply by 100 and check if it's effectively an integer
	scaled := price * 100
	return math.Abs(scaled-math.Round(scaled)) < 0.0001
}

// =============================================
// INPUT SANITIZATION FUNCTIONS
// =============================================

// SanitizeEventInput sanitizes event input fields for XSS prevention
func SanitizeEventInput(name, description, customLocation, dressCode *string) {
	if name != nil {
		*name = sanitizeText(*name, MaxEventNameLength)
	}
	if description != nil {
		*description = sanitizeText(*description, MaxEventDescriptionLength)
	}
	if customLocation != nil {
		*customLocation = sanitizeText(*customLocation, MaxEventLocationLength)
	}
	if dressCode != nil {
		*dressCode = sanitizeText(*dressCode, MaxEventDressCodeLength)
	}
}

// SanitizeTicketTypeInput sanitizes ticket type input fields
func SanitizeTicketTypeInput(name, description, benefits *string) {
	if name != nil {
		*name = sanitizeText(*name, MaxTicketTypeName)
	}
	if description != nil {
		*description = sanitizeText(*description, MaxTicketTypeDescription)
	}
	if benefits != nil {
		*benefits = sanitizeText(*benefits, MaxTicketTypeDescription)
	}
}

// sanitizeText sanitizes a text field
func sanitizeText(input string, maxLen int) string {
	// Escape HTML entities
	sanitized := html.EscapeString(input)

	// Remove any remaining potential XSS patterns
	sanitized = removeXSSPatterns(sanitized)

	// Truncate to max length
	if len(sanitized) > maxLen {
		sanitized = sanitized[:maxLen]
	}

	// Trim whitespace
	sanitized = strings.TrimSpace(sanitized)

	return sanitized
}

// removeXSSPatterns removes common XSS patterns
func removeXSSPatterns(input string) string {
	// Patterns to remove
	xssPatterns := []string{
		"javascript:",
		"vbscript:",
		"data:text/html",
		"onload=",
		"onerror=",
		"onclick=",
		"onmouseover=",
		"onfocus=",
		"onblur=",
	}

	result := strings.ToLower(input)
	for _, pattern := range xssPatterns {
		if strings.Contains(result, pattern) {
			// Replace pattern with empty string in original
			input = regexp.MustCompile("(?i)"+regexp.QuoteMeta(pattern)).ReplaceAllString(input, "")
		}
	}

	return input
}

// =============================================
// AUTHORIZATION HELPERS
// =============================================

// RequireRole checks if the user has one of the allowed roles
func RequireRole(role string, allowedRoles ...string) bool {
	normalizedRole := strings.ToLower(strings.TrimSpace(role))
	for _, allowed := range allowedRoles {
		if normalizedRole == strings.ToLower(allowed) {
			return true
		}
	}
	return false
}

// =============================================
// CACHE INVALIDATION HELPERS
// =============================================

// InvalidateAllEventCaches invalidates all event-related caches for a venue using prefix deletion
func InvalidateAllEventCaches(venueID string) {
	// Use prefix deletion for comprehensive cache invalidation
	services.AppCache.DeletePrefix("public:events:" + venueID)
	services.AppCache.DeletePrefix("public:event:" + venueID)
	services.AppCache.DeletePrefix("staff:events:" + venueID)
}

// InvalidateEventCache invalidates cache for a specific event
func InvalidateEventCache(venueID, eventSlug string) {
	if eventSlug != "" {
		services.AppCache.Delete(fmt.Sprintf("public:event:%s:%s", venueID, eventSlug))
		services.AppCache.Delete(fmt.Sprintf("public:event:%s:%s:tickets", venueID, eventSlug))
	}
	// Also invalidate list caches
	InvalidateAllEventCaches(venueID)
}

// =============================================
// AUDIT LOGGING HELPERS
// =============================================

// LogEventAudit logs an event operation for audit trail
func LogEventAudit(action, eventID, staffID, venueID, details string) {
	log.Printf("[AUDIT] Event %s: event_id=%s staff_id=%s venue_id=%s details=%s",
		action, eventID, staffID, venueID, details)
}

// LogTicketTypeAudit logs a ticket type operation for audit trail
func LogTicketTypeAudit(action, ticketTypeID, eventID, staffID, venueID, details string) {
	log.Printf("[AUDIT] TicketType %s: ticket_type_id=%s event_id=%s staff_id=%s venue_id=%s details=%s",
		action, ticketTypeID, eventID, staffID, venueID, details)
}

// =============================================
// SAFE ERROR HELPERS
// =============================================

// SafeEventError returns a safe error response without leaking internals
func SafeEventError(err error, publicMessage string) string {
	if err != nil {
		log.Printf("[EventController] Internal error: %v", err)
	}
	if config.IsProduction() {
		return publicMessage
	}
	// In development, include error details
	if err != nil {
		return fmt.Sprintf("%s: %v", publicMessage, err)
	}
	return publicMessage
}
