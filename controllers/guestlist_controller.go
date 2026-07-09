package controllers

import (
	"context"
	"fmt"
	"net/http"
	"pull-api-v2/middleware"
	"pull-api-v2/models"
	"pull-api-v2/services"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================
// GUEST LIST CONTROLLERS
// Endpoints for Guest List signups (free entry lists)
// =============================================

// =============================================
// PUBLIC ENDPOINTS
// =============================================

// GetEventGuestLists returns available guest lists for an event.
// Used by the public /guest-lists/event/:eventSlug (WebApp) AND by the
// staff mobile path /guest-lists/types/event/:eventId.
func GetEventGuestLists(c *gin.Context) {
	eventID := c.Param("eventId")
	if eventID == "" {
		eventID = c.Param("event_id")
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Resolve venue from staff context if available, else fall back to the
	// single-tenant Default DB. We deliberately don't go through
	// getEventByIDInternal here — it queries the default DB only and the
	// event_date enum column trips up `select *` on some Supabase rows.
	venueID := c.GetString("venue_id")
	var client *services.SupabaseClient
	if venueID != "" {
		client = services.DB.ForVenue(venueID)
	}
	if client == nil {
		client = services.DB.Default()
	}

	guestListTypes, err := client.QueryCtx(ctx, "guest_list_types", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"event_id":  eventID,
			"is_active": true,
		},
		"order": "name.asc",
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load guest lists", err)
		return
	}

	// Filter out full lists. The schema columns are current_signups /
	// max_signups; expose max_capacity / current_count aliases because the
	// mobile edit form reads those names.
	available := make([]map[string]interface{}, 0)
	for _, gl := range guestListTypes {
		currentCount := services.GetInt(gl, "current_signups")
		maxCapacity := services.GetInt(gl, "max_signups")
		gl["current_count"] = currentCount
		gl["max_capacity"] = maxCapacity
		if maxCapacity == 0 || currentCount < maxCapacity {
			available = append(available, gl)
		}
	}

	// PullMobileApp-GL's guestListService maps response.data straight into
	// state (expects bare array). Returning {data:[...]} would put the
	// wrapper object into state and crash on .map().
	c.JSON(http.StatusOK, available)
}

// GuestListSignup signs up a user for a guest list
// POST /api/v1/guest-lists/signup
func GuestListSignup(c *gin.Context) {
	var req models.GuestListSignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	// Sanitize inputs
	req.Name = middleware.SanitizeName(req.Name)
	req.Surname = middleware.SanitizeName(req.Surname)
	req.Email = middleware.SanitizeEmail(req.Email)

	// Get user ID if authenticated
	var userID *string
	if claims, exists := c.Get("user"); exists {
		userClaims := claims.(*models.UserClaims)
		userID = &userClaims.UserID
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Get event
	event, err := getEventByIDInternal(ctx, req.EventID)
	if err != nil {
		middleware.SafeError(c, http.StatusNotFound, "Event not found", err)
		return
	}

	client := services.DB.ForVenue(event.VenueID)

	// Get guest list type
	guestListType, err := client.QueryOne(ctx, "guest_list_types", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id": req.GuestListTypeID,
		},
	})

	if err != nil || guestListType == nil {
		middleware.SafeError(c, http.StatusNotFound, "Guest list not found", err)
		return
	}

	// Check if list is active
	if !services.GetBool(guestListType, "is_active") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This guest list is no longer accepting signups"})
		return
	}

	// Check capacity (max_signups == 0 means unlimited)
	currentCount := services.GetInt(guestListType, "current_signups")
	maxCapacity := services.GetInt(guestListType, "max_signups")
	if maxCapacity > 0 && currentCount >= maxCapacity {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This guest list is full"})
		return
	}

	// Check for duplicate signup
	existingSignups, _ := client.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"guest_list_type_id": req.GuestListTypeID,
			"email":              req.Email,
		},
	})

	if len(existingSignups) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You have already signed up for this list"})
		return
	}

	// Check gender restriction if any
	genderRestriction := services.GetString(guestListType, "gender")
	if genderRestriction != "" && genderRestriction != "any" && req.Gender != genderRestriction {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("This list is for %s only", genderRestriction)})
		return
	}

	// Determine initial status
	status := "approved"
	if services.GetBool(guestListType, "requires_approval") {
		status = "pending"
	}

	// Create signup data
	signupData := map[string]interface{}{
		"guest_list_type_id": req.GuestListTypeID,
		"event_id":           req.EventID,
		"venue_id":           event.VenueID,
		"name":               req.Name,
		"surname":            req.Surname,
		"email":              req.Email,
		"phone":              req.Phone,
		"gender":             req.Gender,
		"plus_ones":          req.PlusOnes,
		"status":             status,
		"qr_token":           generateSecureToken(),
		"checked_in":         false,
		"plus_ones_used":     0,
		"created_at":         time.Now().Format(time.RFC3339),
		"updated_at":         time.Now().Format(time.RFC3339),
	}

	if userID != nil {
		signupData["user_id"] = *userID
	}
	if req.BirthDate != "" {
		signupData["birth_date"] = req.BirthDate
	}

	result, err := client.InsertCtx(ctx, "guest_list_signups", signupData)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not complete signup", err)
		return
	}

	insertedID := services.GetString(result, "id")
	qrToken := services.GetString(result, "qr_token")

	// Update count
	go updateGuestListCountInternal(event.VenueID, req.GuestListTypeID)

	// Create notification for staff if requires approval
	listName := services.GetString(guestListType, "name")
	if status == "pending" {
		go createGuestListSignupNotificationInternal(event.VenueID, event.OrganizationID, insertedID, req.Name, listName, event.Name)
	} else {
		// Send confirmation email immediately if auto-approved
		go services.Email.SendGuestListConfirmation(req.Email, req.Name, event.Name, listName, qrToken)
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": getSignupMessageInternal(status),
		"data": gin.H{
			"id":       insertedID,
			"status":   status,
			"qr_token": qrToken,
		},
	})
}

// GetGuestListSignupByQR returns signup info by QR token
// GET /api/v1/guest-lists/qr/:token
// OPTIMIZED: Parallel queries for guest_list_type and event
func GetGuestListSignupByQR(c *gin.Context) {
	token := c.Param("token")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// First get signup from central DB
	centralClient := services.DB.Default()
	signup, err := centralClient.QueryOne(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"qr_token": token,
		},
	})

	if err != nil || signup == nil {
		middleware.SafeError(c, http.StatusNotFound, "Signup not found", err)
		return
	}

	// Route to venue-specific DB for related data
	venueID := services.GetString(signup, "venue_id")
	venueClient := services.DB.ForVenue(venueID)
	if venueClient == nil {
		venueClient = centralClient // Fallback
	}

	// OPTIMIZED: Parallel fetch guest_list_type and event
	guestListTypeID := services.GetString(signup, "guest_list_type_id")
	eventID := services.GetString(signup, "event_id")

	var guestListType, event map[string]interface{}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		guestListType, _ = venueClient.QueryOne(ctx, "guest_list_types", map[string]interface{}{
			"select": "id,name,entry_benefit,entry_time",
			"where": map[string]interface{}{
				"id": guestListTypeID,
			},
		})
	}()

	go func() {
		defer wg.Done()
		event, _ = venueClient.QueryOne(ctx, "events", map[string]interface{}{
			"select": "id,name,event_date,start_time,image",
			"where": map[string]interface{}{
				"id": eventID,
			},
		})
	}()

	wg.Wait()

	signup["guest_list_type"] = guestListType
	signup["event"] = event

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    signup,
	})
}

// GetMyGuestListSignups returns user's guest list signups
// GET /api/v1/guest-lists/my
// Cross-venue query with optimized batch fetching
func GetMyGuestListSignups(c *gin.Context) {
	claims, _ := c.Get("user")
	userClaims := claims.(*models.UserClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Cross-venue query from central DB
	centralClient := services.DB.Default()
	signups, err := centralClient.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"user_id": userClaims.UserID,
		},
		"order": "created_at.desc",
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load signups", err)
		return
	}

	// =============================================
	// OPTIMIZED: Batch fetch events and guest list types per venue
	// Groups queries by venue for proper multi-tenant isolation
	// =============================================

	// Group by venue for efficient batch queries
	type venueData struct {
		eventIDs []string
		gltIDs   []string
	}
	venueGroups := make(map[string]*venueData)

	for _, signup := range signups {
		venueID := services.GetString(signup, "venue_id")
		if venueID == "" {
			continue
		}
		if venueGroups[venueID] == nil {
			venueGroups[venueID] = &venueData{
				eventIDs: make([]string, 0),
				gltIDs:   make([]string, 0),
			}
		}
		eventID := services.GetString(signup, "event_id")
		gltID := services.GetString(signup, "guest_list_type_id")
		venueGroups[venueID].eventIDs = append(venueGroups[venueID].eventIDs, eventID)
		venueGroups[venueID].gltIDs = append(venueGroups[venueID].gltIDs, gltID)
	}

	// Parallel fetch from each venue
	eventMap := make(map[string]map[string]interface{})
	gltMap := make(map[string]map[string]interface{})
	var mu sync.Mutex
	var wg sync.WaitGroup

	for venueID, data := range venueGroups {
		wg.Add(1)
		go func(vID string, vData *venueData) {
			defer wg.Done()
			venueClient := services.DB.ForVenue(vID)
			if venueClient == nil {
				venueClient = centralClient // Fallback
			}

			// Fetch events and guest list types for this venue in parallel
			var innerWg sync.WaitGroup
			innerWg.Add(2)

			go func() {
				defer innerWg.Done()
				if len(vData.eventIDs) > 0 {
					events, _ := venueClient.QueryCtx(ctx, "events", map[string]interface{}{
						"select": "id,name,event_date,image",
						"where":  map[string]interface{}{"id": services.FormatInClause(vData.eventIDs)},
					})
					mu.Lock()
					for _, e := range events {
						eventMap[services.GetString(e, "id")] = e
					}
					mu.Unlock()
				}
			}()

			go func() {
				defer innerWg.Done()
				if len(vData.gltIDs) > 0 {
					glts, _ := venueClient.QueryCtx(ctx, "guest_list_types", map[string]interface{}{
						"select": "id,name,entry_benefit",
						"where":  map[string]interface{}{"id": services.FormatInClause(vData.gltIDs)},
					})
					mu.Lock()
					for _, glt := range glts {
						gltMap[services.GetString(glt, "id")] = glt
					}
					mu.Unlock()
				}
			}()

			innerWg.Wait()
		}(venueID, data)
	}

	wg.Wait()

	// Enrich signups with event and guest list type info (O(1) lookups)
	for i, signup := range signups {
		signups[i]["event"] = eventMap[services.GetString(signup, "event_id")]
		signups[i]["guest_list_type"] = gltMap[services.GetString(signup, "guest_list_type_id")]
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    signups,
	})
}

// =============================================
// STAFF ENDPOINTS - Guest List Types
// =============================================

// CreateGuestListType creates a new guest list type for an event
// POST /api/v1/staff/guest-lists/types
func CreateGuestListType(c *gin.Context) {
	var req struct {
		EventID          string  `json:"event_id" binding:"required"`
		Name             string  `json:"name" binding:"required"`
		Description      string  `json:"description"`
		MaxCapacity      int     `json:"max_capacity" binding:"required,min=1"`
		EntryTime        string  `json:"entry_time"`
		EntryBenefit     string  `json:"entry_benefit"`
		RequiresApproval bool    `json:"requires_approval"`
		Gender           *string `json:"gender"`
		ClosesAt         string  `json:"closes_at"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// guest_list_types real columns: event_id, name, description, benefits,
	// max_signups, current_signups, signup_start, signup_end, is_active,
	// created_at. There is no venue_id (per-venue DB), gender, entry_time,
	// requires_approval or updated_at — those request fields are accepted for
	// compatibility but not persisted.
	guestListData := map[string]interface{}{
		"event_id":        req.EventID,
		"name":            middleware.SanitizeName(req.Name),
		"description":     req.Description,
		"max_signups":     req.MaxCapacity,
		"current_signups": 0,
		"is_active":       true,
	}

	if req.EntryBenefit != "" {
		guestListData["benefits"] = req.EntryBenefit
	}
	if req.ClosesAt != "" {
		guestListData["signup_end"] = req.ClosesAt
	}

	result, err := client.InsertCtx(ctx, "guest_list_types", guestListData)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not create guest list", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Guest list created",
		"data":    result,
	})
}

// UpdateGuestListType updates a guest list type
// PUT /api/v1/staff/guest-lists/types/:id (legacy) and
// PUT /api/v1/guest-lists/types/:typeId (mobile app)
func UpdateGuestListType(c *gin.Context) {
	guestListID := c.Param("typeId")
	if guestListID == "" {
		guestListID = c.Param("id")
	}

	var req struct {
		Name             *string `json:"name"`
		Description      *string `json:"description"`
		MaxCapacity      *int    `json:"max_capacity"`
		EntryTime        *string `json:"entry_time"`
		EntryBenefit     *string `json:"entry_benefit"`
		RequiresApproval *bool   `json:"requires_approval"`
		Gender           *string `json:"gender"`
		IsActive         *bool   `json:"is_active"`
		ClosesAt         *string `json:"closes_at"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Only real guest_list_types columns; the table has no updated_at,
	// venue_id, gender or entry_time (see CreateGuestListType).
	updates := map[string]interface{}{}

	if req.Name != nil {
		updates["name"] = middleware.SanitizeName(*req.Name)
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.MaxCapacity != nil {
		updates["max_signups"] = *req.MaxCapacity
	}
	if req.EntryBenefit != nil {
		updates["benefits"] = *req.EntryBenefit
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if req.ClosesAt != nil {
		updates["signup_end"] = *req.ClosesAt
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no updates supplied"})
		return
	}

	// Tenancy is the per-venue database itself — there is no venue_id column
	// to filter on.
	_, err := client.UpdateCtx(ctx, "guest_list_types", updates, map[string]interface{}{
		"id": guestListID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not update guest list", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Guest list updated",
	})
}

// DeleteGuestListType deletes a guest list type
// DELETE /api/v1/staff/guest-lists/types/:id (legacy) and
// DELETE /api/v1/guest-lists/types/:typeId (mobile app)
func DeleteGuestListType(c *gin.Context) {
	guestListID := c.Param("typeId")
	if guestListID == "" {
		guestListID = c.Param("id")
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Soft delete - just deactivate. No updated_at / venue_id columns in
	// guest_list_types (per-venue DB).
	_, err := client.UpdateCtx(ctx, "guest_list_types", map[string]interface{}{
		"is_active": false,
	}, map[string]interface{}{
		"id": guestListID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not delete guest list", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Guest list deleted",
	})
}

// =============================================
// STAFF ENDPOINTS - Signups Management
// =============================================

// GetEventGuestListSignups returns all signups for an event
// GET /api/v1/staff/guest-lists/event/:event_id/signups
func GetEventGuestListSignups(c *gin.Context) {
	eventID := c.Param("event_id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get query parameters for filtering
	status := c.Query("status")
	listTypeID := c.Query("list_type_id")

	whereClause := map[string]interface{}{
		"event_id": eventID,
	}

	if status != "" {
		whereClause["status"] = status
	}
	if listTypeID != "" {
		whereClause["guest_list_type_id"] = listTypeID
	}

	signups, err := client.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where":  whereClause,
		"order":  "created_at.desc",
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load signups", err)
		return
	}

	// Get guest list types for enrichment
	for i, signup := range signups {
		guestListTypeID := services.GetString(signup, "guest_list_type_id")
		guestListType, _ := client.QueryOne(ctx, "guest_list_types", map[string]interface{}{
			"select": "id,name",
			"where":  map[string]interface{}{"id": guestListTypeID},
		})
		signups[i]["guest_list_type"] = guestListType
	}

	// Calculate stats
	var stats struct {
		Total     int `json:"total"`
		Pending   int `json:"pending"`
		Approved  int `json:"approved"`
		CheckedIn int `json:"checked_in"`
		Rejected  int `json:"rejected"`
	}

	stats.Total = len(signups)
	for _, s := range signups {
		signupStatus := services.GetString(s, "status")
		switch signupStatus {
		case "pending":
			stats.Pending++
		case "approved":
			stats.Approved++
		case "checked_in":
			stats.CheckedIn++
		case "rejected":
			stats.Rejected++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    signups,
		"stats":   stats,
	})
}

// ApproveGuestListSignup approves a signup
// POST /api/v1/staff/guest-lists/signups/:id/approve
func ApproveGuestListSignup(c *gin.Context) {
	// Mobile route registers :signupId, legacy route :id — accept both.
	signupID := c.Param("signupId")
	if signupID == "" {
		signupID = c.Param("id")
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get signup first. guest_list_signups has no venue_id column — the
	// per-venue database is the tenancy boundary.
	signup, err := client.QueryOne(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where":  map[string]interface{}{"id": signupID},
	})

	if err != nil || signup == nil {
		middleware.SafeError(c, http.StatusNotFound, "Signup not found", err)
		return
	}

	if services.GetString(signup, "status") != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Signup is not pending"})
		return
	}

	// No updated_at column in guest_list_signups.
	now := time.Now()
	_, err = client.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
		"status":      "approved",
		"approved_by": staffClaims.UserID,
		"approved_at": now.Format(time.RFC3339),
	}, map[string]interface{}{
		"id": signupID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not approve signup", err)
		return
	}

	// Send confirmation email
	go func() {
		email := services.GetString(signup, "email")
		name := services.GetString(signup, "name")
		qrToken := services.GetString(signup, "qr_token")
		eventID := services.GetString(signup, "event_id")
		guestListTypeID := services.GetString(signup, "guest_list_type_id")

		event, _ := client.QueryOne(context.Background(), "events", map[string]interface{}{
			"select": "name",
			"where":  map[string]interface{}{"id": eventID},
		})
		guestListType, _ := client.QueryOne(context.Background(), "guest_list_types", map[string]interface{}{
			"select": "name",
			"where":  map[string]interface{}{"id": guestListTypeID},
		})

		eventName := ""
		listName := ""
		if event != nil {
			eventName = services.GetString(event, "name")
		}
		if guestListType != nil {
			listName = services.GetString(guestListType, "name")
		}

		services.Email.SendGuestListConfirmation(email, name, eventName, listName, qrToken)
	}()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Signup approved",
	})
}

// RejectGuestListSignup rejects a signup
// POST /api/v1/staff/guest-lists/signups/:id/reject
func RejectGuestListSignup(c *gin.Context) {
	// Mobile route registers :signupId, legacy route :id — accept both.
	signupID := c.Param("signupId")
	if signupID == "" {
		signupID = c.Param("id")
	}

	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// guest_list_signups has no rejected_by/rejected_at/reject_reason or
	// updated_at columns — keep the reason (and actor) in notes.
	updates := map[string]interface{}{
		"status": "rejected",
	}
	if req.Reason != "" {
		updates["notes"] = strings.TrimSpace("Rechazada: " + req.Reason + " (staff " + staffClaims.UserID + ")")
	}

	_, err := client.UpdateCtx(ctx, "guest_list_signups", updates, map[string]interface{}{
		"id": signupID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not reject signup", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Signup rejected",
	})
}

// CheckInGuestListSignup checks in a guest list signup
// POST /api/v1/staff/guest-lists/check-in
func CheckInGuestListSignup(c *gin.Context) {
	var req struct {
		QRToken      string `json:"qr_token" binding:"required"`
		PlusOnesUsed int    `json:"plus_ones_used"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "QR token is required", err)
		return
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Find signup
	signup, err := client.QueryOne(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where":  map[string]interface{}{"qr_token": req.QRToken},
	})

	if err != nil || signup == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Signup not found",
		})
		return
	}

	// Validate status
	if services.GetBool(signup, "checked_in") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Already checked in",
			"data": gin.H{
				"name":          services.GetString(signup, "name"),
				"checked_in_at": services.GetString(signup, "checked_in_at"),
			},
		})
		return
	}

	status := services.GetString(signup, "status")
	if status == "rejected" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Signup was rejected",
		})
		return
	}

	if status == "pending" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Signup is pending approval",
		})
		return
	}

	// Validate plus ones
	plusOnes := services.GetInt(signup, "plus_ones")
	plusOnesUsed := req.PlusOnesUsed
	if plusOnesUsed > plusOnes {
		plusOnesUsed = plusOnes
	}

	now := time.Now()
	_, err = client.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
		"checked_in_at": now.Format(time.RFC3339),
		"checked_in_by": staffClaims.UserID,
		"status":        "checked_in",
	}, map[string]interface{}{
		"id": services.GetString(signup, "id"),
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not check in", err)
		return
	}

	// Get guest list type for response
	guestListTypeID := services.GetString(signup, "guest_list_type_id")
	guestListType, _ := client.QueryOne(ctx, "guest_list_types", map[string]interface{}{
		"select": "name,entry_benefit",
		"where":  map[string]interface{}{"id": guestListTypeID},
	})

	entryBenefit := ""
	listName := ""
	if guestListType != nil {
		entryBenefit = services.GetString(guestListType, "entry_benefit")
		listName = services.GetString(guestListType, "name")
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Check-in successful",
		"data": gin.H{
			"name":          services.GetString(signup, "name"),
			"list_name":     listName,
			"plus_ones":     plusOnesUsed,
			"entry_benefit": entryBenefit,
		},
	})
}

// UndoGuestListCheckIn reverts a check-in
// POST /api/v1/staff/guest-lists/signups/:id/undo-checkin
func UndoGuestListCheckIn(c *gin.Context) {
	signupID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	_, err := client.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
		"checked_in_at": nil,
		"checked_in_by": nil,
		"status":        "approved",
	}, map[string]interface{}{
		"id": signupID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not undo check-in", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Check-in reverted",
	})
}

// GetVenuePendingSignups returns pending signups for a venue
// GET /api/v1/guest-lists/venue/:venueId/pending
func GetVenuePendingSignups(c *gin.Context) {
	venueID := c.Param("venueId")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	if staffClaims.VenueID != venueID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Parse pagination
	page := services.GetIntParam(c, "page", 1)
	limit := services.GetIntParam(c, "limit", 20)
	offset := (page - 1) * limit

	client := services.DB.ForVenue(venueID)

	// guest_list_signups has no venue_id column — we resolve the venue's
	// events first and filter signups by event_id.
	events, _ := client.QueryCtx(ctx, "events", map[string]interface{}{
		"select": "id",
	})
	if len(events) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"signups": []interface{}{},
			"count":   0,
			"page":    page,
			"limit":   limit,
		})
		return
	}
	eventIDList := make([]string, 0, len(events))
	for _, ev := range events {
		if id := services.GetString(ev, "id"); id != "" {
			eventIDList = append(eventIDList, id)
		}
	}
	eventInClause := "in.(" + strings.Join(eventIDList, ",") + ")"

	signups, err := client.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"event_id": eventInClause,
			"status":   "pending",
		},
		"order":  "created_at.asc",
		"limit":  limit,
		"offset": offset,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load signups", err)
		return
	}

	// Get total count
	allPending, _ := client.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"event_id": eventInClause,
			"status":   "pending",
		},
	})
	totalCount := len(allPending)

	// Batch fetch events and guest list types
	eventIDs := make([]string, 0)
	gltIDs := make([]string, 0)
	eventSet := make(map[string]bool)
	gltSet := make(map[string]bool)

	for _, s := range signups {
		eid := services.GetString(s, "event_id")
		if eid != "" && !eventSet[eid] {
			eventIDs = append(eventIDs, eid)
			eventSet[eid] = true
		}
		gltid := services.GetString(s, "guest_list_type_id")
		if gltid != "" && !gltSet[gltid] {
			gltIDs = append(gltIDs, gltid)
			gltSet[gltid] = true
		}
	}

	// Parallel fetch
	var eventDetails, glts []map[string]interface{}
	var wg sync.WaitGroup
	var mu sync.Mutex

	if len(eventIDs) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// NOTE: events has no event_date column (it's derived from
			// start_datetime by EnrichEvent) — selecting it 42703s silently.
			result, _ := client.QueryCtx(ctx, "events", map[string]interface{}{
				"select": "id,name,start_datetime,image",
				"where":  map[string]interface{}{"id": services.FormatInClause(eventIDs)},
			})
			services.EnrichEvents(result)
			mu.Lock()
			eventDetails = result
			mu.Unlock()
		}()
	}

	if len(gltIDs) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// NOTE: real column is benefits, not entry_benefit (42703).
			result, _ := client.QueryCtx(ctx, "guest_list_types", map[string]interface{}{
				"select": "id,name,benefits",
				"where":  map[string]interface{}{"id": services.FormatInClause(gltIDs)},
			})
			mu.Lock()
			glts = result
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Build maps
	eventMap := make(map[string]map[string]interface{})
	for _, e := range eventDetails {
		eventMap[services.GetString(e, "id")] = e
	}
	gltMap := make(map[string]map[string]interface{})
	for _, g := range glts {
		gltMap[services.GetString(g, "id")] = g
	}

	// Enrich (plus the flat aliases the ReservasList card reads)
	for i, s := range signups {
		ev := eventMap[services.GetString(s, "event_id")]
		glt := gltMap[services.GetString(s, "guest_list_type_id")]
		signups[i]["event"] = ev
		signups[i]["guest_list_type"] = glt
		if ev != nil {
			signups[i]["event_name"] = services.GetString(ev, "name")
		}
		if glt != nil {
			signups[i]["guest_list_name"] = services.GetString(glt, "name")
		}
	}

	totalPages := (totalCount + limit - 1) / limit

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    signups,
		"pagination": gin.H{
			"current_page": page,
			"total_pages":  totalPages,
			"total_count":  totalCount,
			"has_more":     page < totalPages,
			"limit":        limit,
		},
	})
}

// GetGuestListSignup returns a specific signup by ID
// GET /api/v1/guest-lists/signup/:signupId
func GetGuestListSignup(c *gin.Context) {
	signupID := c.Param("signupId")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	signup, err := client.QueryOne(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where":  map[string]interface{}{"id": signupID},
	})

	if err != nil || signup == nil {
		middleware.SafeError(c, http.StatusNotFound, "Signup not found", err)
		return
	}

	// Get event info (event_date/start_time are derived by EnrichEvent, not
	// real columns)
	event, _ := client.QueryOne(ctx, "events", map[string]interface{}{
		"select": "id,name,start_datetime,end_datetime,image",
		"where":  map[string]interface{}{"id": services.GetString(signup, "event_id")},
	})
	if event != nil {
		services.EnrichEvent(event)
	}
	signup["event"] = event

	// Get guest list type info (real column is benefits)
	glt, _ := client.QueryOne(ctx, "guest_list_types", map[string]interface{}{
		"select": "id,name,benefits",
		"where":  map[string]interface{}{"id": services.GetString(signup, "guest_list_type_id")},
	})
	signup["guest_list_type"] = glt
	if event != nil {
		signup["event_name"] = services.GetString(event, "name")
	}
	if glt != nil {
		signup["guest_list_name"] = services.GetString(glt, "name")
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    signup,
	})
}

// BatchApproveGuestListSignups approves multiple signups at once
// POST /api/v1/guest-lists/batch/approve
func BatchApproveGuestListSignups(c *gin.Context) {
	var req struct {
		SignupIDs []string `json:"signup_ids" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)
	now := time.Now()

	approved := 0
	failed := 0

	for _, signupID := range req.SignupIDs {
		_, err := client.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
			"status":      "approved",
			"approved_by": staffClaims.UserID,
			"approved_at": now.Format(time.RFC3339),
		}, map[string]interface{}{
			"id":     signupID,
			"status": "pending",
		})

		if err != nil {
			failed++
		} else {
			approved++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Approved %d signups, %d failed", approved, failed),
		"data": gin.H{
			"approved": approved,
			"failed":   failed,
		},
	})
}

// BatchRejectGuestListSignups rejects multiple signups at once
// POST /api/v1/guest-lists/batch/reject
func BatchRejectGuestListSignups(c *gin.Context) {
	var req struct {
		SignupIDs []string `json:"signup_ids" binding:"required,min=1"`
		Reason    string   `json:"reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	rejected := 0
	failed := 0

	// guest_list_signups has no rejected_*/updated_at columns.
	updateData := map[string]interface{}{
		"status": "rejected",
	}
	if req.Reason != "" {
		updateData["notes"] = "Rechazada: " + req.Reason
	}

	for _, signupID := range req.SignupIDs {
		_, err := client.UpdateCtx(ctx, "guest_list_signups", updateData, map[string]interface{}{
			"id":     signupID,
			"status": "pending",
		})

		if err != nil {
			failed++
		} else {
			rejected++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Rejected %d signups, %d failed", rejected, failed),
		"data": gin.H{
			"rejected": rejected,
			"failed":   failed,
		},
	})
}

// =============================================
// HELPER FUNCTIONS
// =============================================

func updateGuestListCountInternal(venueID, guestListTypeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Count non-rejected signups
	signups, _ := client.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"guest_list_type_id": guestListTypeID,
		},
	})

	// Count excluding rejected
	count := 0
	for _, s := range signups {
		status := services.GetString(s, "status")
		if status != "rejected" {
			count++
		}
	}

	client.UpdateNoReturn(ctx, "guest_list_types", map[string]interface{}{
		"current_signups": count,
	}, map[string]interface{}{
		"id": guestListTypeID,
	})
}

func createGuestListSignupNotificationInternal(venueID, orgID, signupID, userName, listName, eventName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	notificationData := map[string]interface{}{
		"venue_id":             venueID,
		"organization_id":      orgID,
		"type":                 string(models.NotifyGuestListSignup),
		"priority":             string(models.PriorityNormal),
		"title":                "Nueva inscripción en lista",
		"message":              fmt.Sprintf("%s se inscribió en %s para %s", userName, listName, eventName),
		"target_type":          "staff",
		"target_role":          "admin",
		"guest_list_signup_id": signupID,
		"is_read":              false,
		"created_at":           time.Now().Format(time.RFC3339),
	}

	client.InsertCtx(ctx, "notifications", notificationData)
}

func getSignupMessageInternal(status string) string {
	if status == "pending" {
		return "Signup submitted. Waiting for approval."
	}
	return "Signup confirmed! Check your email for your QR code."
}
