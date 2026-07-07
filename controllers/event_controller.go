package controllers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"pull-api-v2/services"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Cache TTLs for event endpoints - Optimized for read-heavy workloads
const (
	eventListCacheTTL    = 5 * time.Minute  // Longer for public lists
	eventSingleCacheTTL  = 10 * time.Minute // Event details rarely change
	eventTicketsCacheTTL = 2 * time.Minute  // Short for availability
	staffEventCacheTTL   = 30 * time.Second // Staff sees updates faster
)

// Pagination defaults
const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

// =============================================
// PUBLIC EVENT ENDPOINTS
// =============================================

// GetEvents returns a list of public events
// GET /api/v1/events
// OPTIMIZED: Response caching, pagination support
func GetEvents(c *gin.Context) {
	// Require venue_id to know which database to query
	venueID := c.Query("venue_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id is required"})
		return
	}

	// Parse filters and pagination
	dateFilter := c.Query("date")
	upcoming := c.Query("upcoming")
	limit := clampInt(getIntParam(c, "limit", defaultPageLimit), 1, maxPageLimit)
	offset := getIntParam(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	// Check cache first (include pagination in key)
	cacheKey := fmt.Sprintf("public:events:%s:date=%s:upcoming=%s:limit=%d:offset=%d",
		venueID, dateFilter, upcoming, limit, offset)
	if cached, ok := services.AppCache.Get(cacheKey); ok {
		if data, ok := cached.(map[string]interface{}); ok {
			data["cached"] = true
			c.JSON(http.StatusOK, data)
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	whereClause := map[string]interface{}{
		"status":     services.PublishedEventStatuses,
		"deleted_at": "is.null",
	}

	if dateFilter != "" {
		// Same-day filter on start_datetime
		whereClause["start_datetime"] = "gte." + dateFilter + "T00:00:00Z"
	} else if upcoming == "true" || upcoming == "" {
		whereClause["start_datetime"] = "gte." + time.Now().Format(time.RFC3339)
	}

	// Query events with pagination - OPTIMIZED: minimal fields for list view
	events, err := venueDB.QueryCtx(ctx, "events", map[string]interface{}{
		"select": services.EventListSelectColumns,
		"where":  whereClause,
		"order":  "start_datetime.asc",
		"limit":  limit,
		"offset": offset,
	})

	if err != nil {
		log.Printf("[GetEvents] Database error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get events"})
		return
	}

	services.EnrichEvents(events)

	result := map[string]interface{}{
		"events": events,
		"count":  len(events),
		"limit":  limit,
		"offset": offset,
	}

	// Cache the result
	services.AppCache.Set(cacheKey, result, eventListCacheTTL)

	c.JSON(http.StatusOK, result)
}

// GetEvent returns an event by slug
// GET /api/v1/events/:slug
// OPTIMIZED: Response caching
func GetEvent(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Event slug is required"})
		return
	}

	venueID := c.Query("venue_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id is required"})
		return
	}

	// Check cache first
	cacheKey := fmt.Sprintf("public:event:%s:%s", venueID, slug)
	if cached, ok := services.AppCache.Get(cacheKey); ok {
		if event, ok := cached.(map[string]interface{}); ok {
			c.JSON(http.StatusOK, gin.H{
				"event":  event,
				"cached": true,
			})
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	// Query event
	event, err := venueDB.QueryOne(ctx, "events", map[string]interface{}{
		"select": services.EventSelectColumns,
		"where": map[string]interface{}{
			"slug":       slug,
			"status":     services.PublishedEventStatuses,
			"deleted_at": "is.null",
		},
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get event"})
		return
	}

	if event == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}

	services.EnrichEvent(event)

	// Cache the result
	services.AppCache.Set(cacheKey, event, eventSingleCacheTTL)

	c.JSON(http.StatusOK, gin.H{
		"event": event,
	})
}

// GetEventTickets returns ticket types for an event
// GET /api/v1/events/:slug/tickets
// OPTIMIZED: Response caching + parallel ticket type queries
func GetEventTickets(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Event slug is required"})
		return
	}

	venueID := c.Query("venue_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id is required"})
		return
	}

	// Check cache first
	cacheKey := fmt.Sprintf("public:event:%s:%s:tickets", venueID, slug)
	if cached, ok := services.AppCache.Get(cacheKey); ok {
		if data, ok := cached.(map[string]interface{}); ok {
			data["cached"] = true
			c.JSON(http.StatusOK, data)
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	// First, get event ID
	event, err := venueDB.QueryOne(ctx, "events", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"slug":       slug,
			"status":     services.PublishedEventStatuses,
			"deleted_at": "is.null",
		},
	})

	if err != nil || event == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}

	eventID := services.GetString(event, "id")

	// OPTIMIZATION: Parallel fetch ticket types and VIP types
	var ticketTypes, vipTypes []map[string]interface{}
	var ticketErr error
	var wg sync.WaitGroup

	wg.Add(2)

	// Get regular ticket types
	go func() {
		defer wg.Done()
		ticketTypes, ticketErr = venueDB.QueryCtx(ctx, "ticket_types", map[string]interface{}{
			"select": services.TicketTypeSelectColumns,
			"where": map[string]interface{}{
				"event_id":  eventID,
				"is_active": true,
			},
			"order": "sort_order.asc,price.asc",
		})
	}()

	// Get VIP ticket types
	go func() {
		defer wg.Done()
		vipTypes, _ = venueDB.QueryCtx(ctx, "event_vip_ticket_types", map[string]interface{}{
			"select": services.VIPTicketTypeSelectColumns,
			"where": map[string]interface{}{
				"event_id":  eventID,
				"is_active": true,
			},
			"order": "sort_order.asc",
		})
	}()

	wg.Wait()

	if ticketErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get ticket types"})
		return
	}

	services.EnrichTicketTypes(ticketTypes)
	services.EnrichVIPTicketTypes(vipTypes)

	result := map[string]interface{}{
		"ticket_types":     ticketTypes,
		"vip_ticket_types": vipTypes,
		"event_id":         eventID,
	}

	// Cache the result (short TTL for availability)
	services.AppCache.Set(cacheKey, result, eventTicketsCacheTTL)

	c.JSON(http.StatusOK, result)
}

// =============================================
// STAFF EVENT ENDPOINTS (Authenticated)
// =============================================

// GetStaffEvents returns all events for a venue (staff)
// GET /api/v1/staff/events
func GetStaffEvents(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.GetString("venue_id")
	if venueID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Venue database not available"})
		return
	}

	// Parse filters
	status := c.Query("status")
	published := c.Query("published")

	whereClause := map[string]interface{}{
		"deleted_at": "is.null",
	}

	if status == "active" {
		whereClause["status"] = "in.(published,active,live,draft)"
	} else if status == "inactive" {
		whereClause["status"] = "in.(archived,cancelled)"
	}

	if published == "true" {
		whereClause["status"] = services.PublishedEventStatuses
	} else if published == "false" {
		whereClause["status"] = "eq.draft"
	}

	// Query events
	events, err := venueDB.QueryCtx(ctx, "events", map[string]interface{}{
		"select": services.EventSelectColumns + ",created_at",
		"where":  whereClause,
		"order":  "start_datetime.desc",
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get events"})
		return
	}

	services.EnrichEvents(events)

	c.JSON(http.StatusOK, gin.H{
		"events": events,
		"count":  len(events),
	})
}

// CreateEventRequest represents the create event request
// SECURITY: All fields validated before database insertion
type CreateEventRequest struct {
	Name           string `json:"name" binding:"required,min=2,max=255"`
	Slug           string `json:"slug" binding:"omitempty,min=2,max=255"`
	Description    string `json:"description" binding:"max=5000"`
	Image          string `json:"image" binding:"omitempty,max=2000"`
	EventDate      string `json:"event_date" binding:"required"`
	StartTime      string `json:"start_time" binding:"required"`
	EndTime        string `json:"end_time" binding:"required"`
	CustomLocation string `json:"custom_location" binding:"max=500"`
	TicketLimit    int    `json:"ticket_limit" binding:"gte=0,lte=100000"`
	TableCapacity  int    `json:"table_capacity" binding:"gte=0,lte=10000"`
	MinAge         int    `json:"min_age" binding:"gte=0,lte=120"`
	DressCode      string `json:"dress_code" binding:"max=500"`
	IsPublished    bool   `json:"is_published"`
}

// CreateEvent creates a new event (staff)
// POST /api/v1/event
// SECURITY: Input validation, XSS sanitization, audit logging
func CreateEvent(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.GetString("venue_id")
	staffID := c.GetString("staff_id")
	role := c.GetString("role")

	if venueID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	// SECURITY: Role-based authorization
	if !RequireRole(role, "admin", "manager") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		return
	}

	var req CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	// SECURITY: Validate date/time fields
	if err := ValidateEventDateTime(req.EventDate, req.StartTime, req.EndTime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// SECURITY: Validate numeric limits
	if err := ValidateEventLimits(req.TicketLimit, req.TableCapacity, req.MinAge); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// SECURITY: Validate slug format if provided
	if err := ValidateSlug(req.Slug); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// SECURITY: Sanitize text inputs for XSS prevention
	SanitizeEventInput(&req.Name, &req.Description, &req.CustomLocation, &req.DressCode)

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Venue database not available"})
		return
	}

	// Generate slug if not provided
	if req.Slug == "" {
		req.Slug = generateSlug(req.Name, req.EventDate)
	}

	// Set defaults
	if req.MinAge == 0 {
		req.MinAge = 18
	}

	// Create event
	eventData := map[string]interface{}{
		"name":            req.Name,
		"slug":            req.Slug,
		"description":     req.Description,
		"image":           req.Image,
		"event_date":      req.EventDate,
		"start_time":      req.StartTime,
		"end_time":        req.EndTime,
		"custom_location": req.CustomLocation,
		"ticket_limit":    req.TicketLimit,
		"table_capacity":  req.TableCapacity,
		"min_age":         req.MinAge,
		"dress_code":      req.DressCode,
		"is_active":       true,
		"is_published":    req.IsPublished,
	}

	if req.IsPublished {
		eventData["published_at"] = time.Now().Format(time.RFC3339)
	}

	event, err := venueDB.InsertCtx(ctx, "events", eventData)
	if err != nil {
		log.Printf("[CreateEvent] Database error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create event"})
		return
	}

	// AUDIT: Log event creation
	eventID := services.GetString(event, "id")
	LogEventAudit("CREATE", eventID, staffID, venueID, fmt.Sprintf("Created event: %s", req.Name))

	// Invalidate events cache for this venue (using prefix deletion)
	InvalidateAllEventCaches(venueID)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Event created successfully",
		"event":   event,
	})
}

// UpdateEventRequest represents the update event request
// SECURITY: All fields validated before database update
type UpdateEventRequest struct {
	Name           string `json:"name" binding:"omitempty,min=2,max=255"`
	Slug           string `json:"slug" binding:"omitempty,min=2,max=255"`
	Description    string `json:"description" binding:"max=5000"`
	Image          string `json:"image" binding:"omitempty,max=2000"`
	EventDate      string `json:"event_date"`
	StartTime      string `json:"start_time"`
	EndTime        string `json:"end_time"`
	CustomLocation string `json:"custom_location" binding:"max=500"`
	TicketLimit    *int   `json:"ticket_limit" binding:"omitempty,gte=0,lte=100000"`
	TableCapacity  *int   `json:"table_capacity" binding:"omitempty,gte=0,lte=10000"`
	MinAge         *int   `json:"min_age" binding:"omitempty,gte=0,lte=120"`
	DressCode      string `json:"dress_code" binding:"max=500"`
	IsActive       *bool  `json:"is_active"`
	IsPublished    *bool  `json:"is_published"`
}

// UpdateEvent updates an event (staff)
// PUT /api/v1/event/:id
// SECURITY: Input validation, ownership verification, audit logging
func UpdateEvent(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.GetString("venue_id")
	staffID := c.GetString("staff_id")
	role := c.GetString("role")
	eventID := c.Param("id")

	if venueID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Event ID is required"})
		return
	}

	// SECURITY: Role-based authorization
	if !RequireRole(role, "admin", "manager") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		return
	}

	var req UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Venue database not available"})
		return
	}

	// SECURITY: Fetch current event to verify ownership and get original slug
	currentEvent, err := venueDB.QueryOne(ctx, "events", map[string]interface{}{
		"select": "id,slug,event_date,start_time,end_time",
		"where": map[string]interface{}{
			"id":         eventID,
			"deleted_at": "is.null",
		},
	})
	if err != nil || currentEvent == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}
	originalSlug := services.GetString(currentEvent, "slug")

	// SECURITY: Validate date/time if any are being updated
	eventDate := req.EventDate
	startTime := req.StartTime
	endTime := req.EndTime

	// Use current values if not being updated
	if eventDate == "" {
		eventDate = services.GetString(currentEvent, "event_date")
	}
	if startTime == "" {
		startTime = services.GetString(currentEvent, "start_time")
	}
	if endTime == "" {
		endTime = services.GetString(currentEvent, "end_time")
	}

	// Only validate if at least one time field is provided
	if req.EventDate != "" || req.StartTime != "" || req.EndTime != "" {
		if err := ValidateEventDateTime(eventDate, startTime, endTime); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// SECURITY: Validate numeric limits if provided
	ticketLimit := 0
	tableCapacity := 0
	minAge := 0
	if req.TicketLimit != nil {
		ticketLimit = *req.TicketLimit
	}
	if req.TableCapacity != nil {
		tableCapacity = *req.TableCapacity
	}
	if req.MinAge != nil {
		minAge = *req.MinAge
	}
	if req.TicketLimit != nil || req.TableCapacity != nil || req.MinAge != nil {
		if err := ValidateEventLimits(ticketLimit, tableCapacity, minAge); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// SECURITY: Validate slug format if provided
	if req.Slug != "" {
		if err := ValidateSlug(req.Slug); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// SECURITY: Sanitize text inputs
	if req.Name != "" {
		SanitizeEventInput(&req.Name, nil, nil, nil)
	}
	if req.Description != "" {
		SanitizeEventInput(nil, &req.Description, nil, nil)
	}
	if req.CustomLocation != "" {
		SanitizeEventInput(nil, nil, &req.CustomLocation, nil)
	}
	if req.DressCode != "" {
		SanitizeEventInput(nil, nil, nil, &req.DressCode)
	}

	// Build update data
	updateData := make(map[string]interface{})
	if req.Name != "" {
		updateData["name"] = req.Name
	}
	if req.Slug != "" {
		updateData["slug"] = req.Slug
	}
	if req.Description != "" {
		updateData["description"] = req.Description
	}
	if req.Image != "" {
		updateData["image"] = req.Image
	}
	if req.EventDate != "" {
		updateData["event_date"] = req.EventDate
	}
	if req.StartTime != "" {
		updateData["start_time"] = req.StartTime
	}
	if req.EndTime != "" {
		updateData["end_time"] = req.EndTime
	}
	if req.CustomLocation != "" {
		updateData["custom_location"] = req.CustomLocation
	}
	if req.TicketLimit != nil {
		updateData["ticket_limit"] = *req.TicketLimit
	}
	if req.TableCapacity != nil {
		updateData["table_capacity"] = *req.TableCapacity
	}
	if req.MinAge != nil {
		updateData["min_age"] = *req.MinAge
	}
	if req.DressCode != "" {
		updateData["dress_code"] = req.DressCode
	}
	if req.IsActive != nil {
		updateData["is_active"] = *req.IsActive
	}
	if req.IsPublished != nil {
		updateData["is_published"] = *req.IsPublished
		if *req.IsPublished {
			updateData["published_at"] = time.Now().Format(time.RFC3339)
		}
	}

	if len(updateData) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	updateData["updated_at"] = time.Now().Format(time.RFC3339)

	// Update event
	result, err := venueDB.UpdateCtx(ctx, "events", updateData, map[string]interface{}{
		"id":         eventID,
		"deleted_at": "is.null",
	})

	if err != nil {
		log.Printf("[UpdateEvent] Database error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update event"})
		return
	}

	if len(result) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}

	// AUDIT: Log event update
	LogEventAudit("UPDATE", eventID, staffID, venueID, fmt.Sprintf("Updated %d fields", len(updateData)-1))

	// Invalidate caches - both old and new slug if changed
	InvalidateEventCache(venueID, originalSlug)
	if newSlug := services.GetString(result[0], "slug"); newSlug != "" && newSlug != originalSlug {
		InvalidateEventCache(venueID, newSlug)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Event updated successfully",
		"event":   result[0],
	})
}

// DeleteEvent soft-deletes an event (staff)
// DELETE /api/v1/event/:id
func DeleteEvent(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.GetString("venue_id")
	role := c.GetString("role")
	eventID := c.Param("id")

	if venueID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Event ID is required"})
		return
	}

	// Only admin can delete events
	if role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		return
	}

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Venue database not available"})
		return
	}

	// Soft delete
	result, err := venueDB.UpdateCtx(ctx, "events", map[string]interface{}{
		"deleted_at": time.Now().Format(time.RFC3339),
		"is_active":  false,
	}, map[string]interface{}{
		"id":         eventID,
		"deleted_at": "is.null",
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete event"})
		return
	}

	if len(result) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}

	// Invalidate events cache
	invalidateEventCaches(venueID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Event deleted successfully",
	})
}

// =============================================
// TICKET TYPE MANAGEMENT (Staff)
// =============================================

// CreateTicketTypeRequest represents the create ticket type request
// SECURITY: Price and quantity validation enforced
type CreateTicketTypeRequest struct {
	EventID          string  `json:"event_id" binding:"required,uuid"`
	Name             string  `json:"name" binding:"required,min=2,max=100"`
	Description      string  `json:"description" binding:"max=1000"`
	Benefits         string  `json:"benefits" binding:"max=1000"`
	Price            float64 `json:"price" binding:"required,gte=0,lte=999999.99"`
	HasGenderPricing bool    `json:"has_gender_pricing"`
	MalePrice        float64 `json:"male_price" binding:"gte=0,lte=999999.99"`
	FemalePrice      float64 `json:"female_price" binding:"gte=0,lte=999999.99"`
	InitialQuantity  int     `json:"initial_quantity" binding:"required,gte=1,lte=50000"`
	MinQuantity      int     `json:"min_quantity" binding:"gte=0,lte=100"`
	MaxQuantity      int     `json:"max_quantity" binding:"gte=0,lte=100"`
	IsGroup          bool    `json:"is_group"`
	SortOrder        int     `json:"sort_order" binding:"gte=0,lte=1000"`
}

// CreateTicketType creates a ticket type for an event
// POST /api/v1/event/:id/ticket-types
// SECURITY: Price validation, event ownership verification, audit logging
func CreateTicketType(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.GetString("venue_id")
	staffID := c.GetString("staff_id")
	role := c.GetString("role")

	if venueID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	// SECURITY: Role-based authorization
	if !RequireRole(role, "admin", "manager") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		return
	}

	var req CreateTicketTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Venue database not available"})
		return
	}

	// SECURITY: Verify event exists and belongs to this venue
	event, err := venueDB.QueryOne(ctx, "events", map[string]interface{}{
		"select": "id,slug",
		"where": map[string]interface{}{
			"id":         req.EventID,
			"deleted_at": "is.null",
		},
	})
	if err != nil || event == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}
	eventSlug := services.GetString(event, "slug")

	// SECURITY: Validate pricing logic
	if err := ValidateTicketPricing(req.Price, req.MalePrice, req.FemalePrice, req.HasGenderPricing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set defaults for quantity
	if req.MinQuantity == 0 {
		req.MinQuantity = 1
	}
	if req.MaxQuantity == 0 {
		req.MaxQuantity = 10
	}

	// SECURITY: Validate quantity logic
	if err := ValidateTicketQuantity(req.InitialQuantity, req.MinQuantity, req.MaxQuantity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// SECURITY: Sanitize text inputs
	SanitizeTicketTypeInput(&req.Name, &req.Description, &req.Benefits)

	// Create ticket type
	ticketType, err := venueDB.InsertCtx(ctx, "ticket_types", map[string]interface{}{
		"event_id":           req.EventID,
		"name":               req.Name,
		"description":        req.Description,
		"benefits":           req.Benefits,
		"price":              req.Price,
		"base_price":         req.Price,
		"has_gender_pricing": req.HasGenderPricing,
		"male_price":         req.MalePrice,
		"female_price":       req.FemalePrice,
		"initial_quantity":   req.InitialQuantity,
		"available_quantity": req.InitialQuantity,
		"min_quantity":       req.MinQuantity,
		"max_quantity":       req.MaxQuantity,
		"is_group":           req.IsGroup,
		"sort_order":         req.SortOrder,
		"is_active":          true,
	})

	if err != nil {
		log.Printf("[CreateTicketType] Database error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ticket type"})
		return
	}

	// AUDIT: Log ticket type creation
	ticketTypeID := services.GetString(ticketType, "id")
	LogTicketTypeAudit("CREATE", ticketTypeID, req.EventID, staffID, venueID,
		fmt.Sprintf("Created ticket: %s at price %.2f", req.Name, req.Price))

	// Invalidate caches including specific event tickets cache
	InvalidateEventCache(venueID, eventSlug)

	c.JSON(http.StatusCreated, gin.H{
		"message":     "Ticket type created successfully",
		"ticket_type": ticketType,
	})
}

// =============================================
// HELPERS
// =============================================

// generateSlug generates a URL-friendly slug from name and date
// SECURITY: Sanitizes input and ensures unique format
func generateSlug(name, date string) string {
	// Convert to lowercase and replace spaces with hyphens
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")

	// Remove special characters (keep only alphanumeric and hyphens)
	var result strings.Builder
	for _, ch := range slug {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			result.WriteRune(ch)
		}
	}

	baseSlug := result.String()
	if baseSlug == "" {
		baseSlug = "event" // Fallback for all-special-char names
	}

	// Remove consecutive hyphens
	for strings.Contains(baseSlug, "--") {
		baseSlug = strings.ReplaceAll(baseSlug, "--", "-")
	}

	// Trim hyphens from start and end
	baseSlug = strings.Trim(baseSlug, "-")

	// Add date suffix for uniqueness
	if date != "" {
		datePart := strings.ReplaceAll(date, "-", "")
		return baseSlug + "-" + datePart
	}

	return baseSlug
}

// invalidateEventCaches invalidates all event-related caches for a venue
// DEPRECATED: Use InvalidateAllEventCaches instead for comprehensive invalidation
func invalidateEventCaches(venueID string) {
	InvalidateAllEventCaches(venueID)
}

// getIntParam safely parses an integer query parameter with default
func getIntParam(c *gin.Context, name string, defaultVal int) int {
	val := c.Query(name)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}

// clampInt clamps a value between min and max
func clampInt(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}
