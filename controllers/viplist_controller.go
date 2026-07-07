package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
// VIP LIST CONTROLLERS
// Endpoints for VIP List reservations
// =============================================

// =============================================
// PUBLIC ENDPOINTS (User App)
// =============================================

// CreateVIPList creates a new VIP list reservation
// POST /api/v1/vip-lists
func CreateVIPList(c *gin.Context) {
	var req models.CreateVIPListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	// Sanitize inputs
	req.OrganizerName = middleware.SanitizeName(req.OrganizerName)
	req.OrganizerEmail = middleware.SanitizeEmail(req.OrganizerEmail)
	req.ListName = middleware.SanitizeName(req.ListName)

	// Get user from context (optional - can create without being logged in)
	var userID string
	if claims, exists := c.Get("user"); exists {
		userClaims := claims.(*models.UserClaims)
		userID = userClaims.UserID
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Get event details to validate and get venue info
	event, err := getEventByIDInternal(ctx, req.EventID)
	if err != nil {
		middleware.SafeError(c, http.StatusNotFound, "Event not found", err)
		return
	}

	// Check if venue allows VIP lists
	venue, err := services.DB.GetVenue(ctx, event.VenueID)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load venue", err)
		return
	}

	if !venue.UseVipListFlow {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VIP lists are not enabled for this venue"})
		return
	}

	// Get VIP list pricing for the event
	pricing := getVIPListPricingInternal(ctx, event.VenueID, req.EventID)

	// Generate unique QR token
	qrToken := generateSecureToken()

	// Create VIP list reservation data
	vipListData := map[string]interface{}{
		"event_id":          req.EventID,
		"venue_id":          event.VenueID,
		"organization_id":   event.OrganizationID,
		"organizer_user_id": userID,
		"list_name":         req.ListName,
		"status":            string(models.ReservationStatusPending),
		"max_guests":        req.MaxGuests,
		"current_guests":    0,
		"price_per_person":  pricing,
		"organizer_pays":    req.OrganizerPays,
		"total_amount":      0,
		"amount_paid":       0,
		"organizer_name":    req.OrganizerName,
		"organizer_email":   req.OrganizerEmail,
		"organizer_phone":   req.OrganizerPhone,
		"qr_token":          qrToken,
		"requires_approval": venue.RequireApprovalOrders,
		"created_at":        time.Now().Format(time.RFC3339),
		"updated_at":        time.Now().Format(time.RFC3339),
	}

	if req.SpecialRequests != "" {
		vipListData["special_requests"] = req.SpecialRequests
	}

	// Get database client for venue
	client := services.DB.ForVenue(event.VenueID)
	if client == nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Database error", fmt.Errorf("no client for venue"))
		return
	}

	// Insert VIP list
	result, err := client.InsertCtx(ctx, "vip_list_reservations", vipListData)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not create VIP list", err)
		return
	}

	insertedID := services.GetString(result, "id")

	// Add organizer as first guest
	organizerGuestData := map[string]interface{}{
		"vip_list_reservation_id": insertedID,
		"user_id":                 userID,
		"name":                    req.OrganizerName,
		"email":                   req.OrganizerEmail,
		"phone":                   req.OrganizerPhone,
		"is_organizer":            true,
		"status":                  string(models.ReservationStatusConfirmed),
		"qr_token":                generateSecureToken(),
		"amount_to_pay":           pricing,
		"amount_paid":             0,
		"checked_in":              false,
		"created_at":              time.Now().Format(time.RFC3339),
		"updated_at":              time.Now().Format(time.RFC3339),
	}

	_, err = client.InsertCtx(ctx, "vip_list_guests", organizerGuestData)
	if err != nil {
		// Log but don't fail
		fmt.Printf("Warning: Could not add organizer as guest: %v\n", err)
	}

	// Add initial guests if provided
	if len(req.Guests) > 0 {
		go addVIPListGuestsInternal(event.VenueID, insertedID, req.Guests, pricing)
	}

	// Create notification for staff
	go createVIPListNotificationInternal(event.VenueID, event.OrganizationID, insertedID, req.OrganizerName, event.Name)

	// Return response
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "VIP list created successfully",
		"data": gin.H{
			"id":               insertedID,
			"qr_token":         qrToken,
			"status":           models.ReservationStatusPending,
			"requires_payment": pricing > 0,
			"price_per_person": pricing,
		},
	})
}

// GetMyVIPLists returns VIP lists for the authenticated user
// GET /api/v1/vip-lists/my
func GetMyVIPLists(c *gin.Context) {
	claims, _ := c.Get("user")
	userClaims := claims.(*models.UserClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Query central DB for user's VIP list references (cross-venue query)
	centralClient := services.DB.Default()
	results, err := centralClient.QueryCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"organizer_user_id": userClaims.UserID,
		},
		"order": "created_at.desc",
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load VIP lists", err)
		return
	}

	// =============================================
	// OPTIMIZED: Batch fetch events by venue (eliminates N+1)
	// =============================================
	if len(results) > 0 {
		// Group by venue for efficient batch queries
		venueEventIDs := make(map[string][]string)
		for _, vipList := range results {
			venueID := services.GetString(vipList, "venue_id")
			eventID := services.GetString(vipList, "event_id")
			if venueID != "" && eventID != "" {
				venueEventIDs[venueID] = append(venueEventIDs[venueID], eventID)
			}
		}

		// Batch fetch events per venue in parallel
		eventMap := make(map[string]map[string]interface{})
		var mu sync.Mutex
		var wg sync.WaitGroup

		for venueID, eventIDs := range venueEventIDs {
			wg.Add(1)
			go func(vID string, eIDs []string) {
				defer wg.Done()
				venueClient := services.DB.ForVenue(vID)
				if venueClient == nil {
					return
				}
				events, _ := venueClient.QueryCtx(ctx, "events", map[string]interface{}{
					"select": "id,name,event_date,start_time,image",
					"where": map[string]interface{}{
						"id": services.FormatInClause(eIDs),
					},
				})
				mu.Lock()
				for _, event := range events {
					eventMap[services.GetString(event, "id")] = event
				}
				mu.Unlock()
			}(venueID, eventIDs)
		}
		wg.Wait()

		// Attach event data to results
		for _, vipList := range results {
			eventID := services.GetString(vipList, "event_id")
			if event, ok := eventMap[eventID]; ok {
				vipList["event"] = event
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    results,
	})
}

// GetVIPList returns a specific VIP list by ID
// GET /api/v1/vip-lists/:id
func GetVIPList(c *gin.Context) {
	vipListID := c.Param("id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// First get VIP list from central DB to find venue_id
	centralClient := services.DB.Default()
	result, err := centralClient.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id": vipListID,
		},
	})

	if err != nil || result == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	// Route to venue-specific DB for related data
	venueID := services.GetString(result, "venue_id")
	venueClient := services.DB.ForVenue(venueID)
	if venueClient == nil {
		venueClient = centralClient // Fallback
	}

	// Parallel fetch: guests and event
	var guests []map[string]interface{}
	var event map[string]interface{}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		guests, _ = venueClient.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
			"select": "*",
			"where": map[string]interface{}{
				"vip_list_reservation_id": vipListID,
			},
			"order": "created_at.asc",
		})
	}()

	go func() {
		defer wg.Done()
		eventID := services.GetString(result, "event_id")
		event, _ = venueClient.QueryOne(ctx, "events", map[string]interface{}{
			"select": "id,name,event_date,start_time,image",
			"where": map[string]interface{}{
				"id": eventID,
			},
		})
	}()

	wg.Wait()

	result["guests"] = guests
	result["event"] = event

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// GetVIPListByQR returns VIP list info by QR token
// GET /api/v1/vip-lists/qr/:token
func GetVIPListByQR(c *gin.Context) {
	token := c.Param("token")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// First lookup in central DB to get venue_id
	centralClient := services.DB.Default()
	result, err := centralClient.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"qr_token": token,
		},
	})

	if err != nil || result == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	// Route to venue-specific DB for related data
	venueID := services.GetString(result, "venue_id")
	venueClient := services.DB.ForVenue(venueID)
	if venueClient == nil {
		venueClient = centralClient // Fallback
	}

	// OPTIMIZED: Parallel fetch event and guests
	vipListID := services.GetString(result, "id")
	eventID := services.GetString(result, "event_id")

	var event map[string]interface{}
	var guests []map[string]interface{}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		event, _ = venueClient.QueryOne(ctx, "events", map[string]interface{}{
			"select": "id,name,event_date,start_time,image",
			"where": map[string]interface{}{
				"id": eventID,
			},
		})
	}()

	go func() {
		defer wg.Done()
		guests, _ = venueClient.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
			"select": "*",
			"where": map[string]interface{}{
				"vip_list_reservation_id": vipListID,
			},
		})
	}()

	wg.Wait()

	result["event"] = event
	result["guests"] = guests

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// AddGuestToVIPList adds a guest to an existing VIP list
// POST /api/v1/vip-lists/:id/guests
func AddGuestToVIPList(c *gin.Context) {
	vipListID := c.Param("id")

	var req models.CreateVIPListGuestInput
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	req.Name = middleware.SanitizeName(req.Name)
	req.Email = middleware.SanitizeEmail(req.Email)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// First get VIP list to find venue_id
	centralClient := services.DB.Default()
	vipList, err := centralClient.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id": vipListID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	// Route to venue-specific DB
	venueID := services.GetString(vipList, "venue_id")
	venueClient := services.DB.ForVenue(venueID)
	if venueClient == nil {
		venueClient = centralClient // Fallback
	}

	// Check capacity
	currentGuests := services.GetInt(vipList, "current_guests")
	maxGuests := services.GetInt(vipList, "max_guests")
	if currentGuests >= maxGuests {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VIP list is at maximum capacity"})
		return
	}

	// Check for duplicate email using venue-specific DB
	existingGuests, _ := venueClient.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"vip_list_reservation_id": vipListID,
			"email":                   req.Email,
		},
	})

	if len(existingGuests) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Guest with this email already exists"})
		return
	}

	pricePerPerson := services.GetFloat64(vipList, "price_per_person")
	qrToken := generateSecureToken()

	// Create guest in venue-specific DB
	guestData := map[string]interface{}{
		"vip_list_reservation_id": vipListID,
		"name":                    req.Name,
		"email":                   req.Email,
		"phone":                   req.Phone,
		"gender":                  req.Gender,
		"is_organizer":            false,
		"status":                  string(models.ReservationStatusPending),
		"qr_token":                qrToken,
		"amount_to_pay":           pricePerPerson,
		"amount_paid":             0,
		"checked_in":              false,
		"created_at":              time.Now().Format(time.RFC3339),
		"updated_at":              time.Now().Format(time.RFC3339),
	}

	result, err := venueClient.InsertCtx(ctx, "vip_list_guests", guestData)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not add guest", err)
		return
	}

	insertedID := services.GetString(result, "id")

	// Update guest count (fire-and-forget)
	go updateVIPListGuestCountInternal(venueID, vipListID)

	// Send invitation email
	organizerName := services.GetString(vipList, "organizer_name")
	listName := services.GetString(vipList, "list_name")
	go services.Email.SendVIPListInvitation(req.Email, req.Name, organizerName, listName, qrToken)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Guest added successfully",
		"data": gin.H{
			"id":       insertedID,
			"qr_token": qrToken,
		},
	})
}

// RemoveGuestFromVIPList removes a guest from a VIP list
// DELETE /api/v1/vip-lists/:id/guests/:guest_id
func RemoveGuestFromVIPList(c *gin.Context) {
	vipListID := c.Param("id")
	guestID := c.Param("guest_id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// First get VIP list to find venue_id
	centralClient := services.DB.Default()
	vipList, err := centralClient.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "venue_id",
		"where":  map[string]interface{}{"id": vipListID},
	})

	if err != nil || vipList == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VIP list not found"})
		return
	}

	// Route to venue-specific DB
	venueID := services.GetString(vipList, "venue_id")
	venueClient := services.DB.ForVenue(venueID)
	if venueClient == nil {
		venueClient = centralClient // Fallback
	}

	// Get guest from venue-specific DB
	guest, err := venueClient.QueryOne(ctx, "vip_list_guests", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id":                      guestID,
			"vip_list_reservation_id": vipListID,
		},
	})

	if err != nil || guest == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Guest not found"})
		return
	}

	if services.GetBool(guest, "is_organizer") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove the organizer"})
		return
	}

	if services.GetBool(guest, "checked_in") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove a checked-in guest"})
		return
	}

	// Delete from venue-specific DB
	err = venueClient.DeleteCtx(ctx, "vip_list_guests", map[string]interface{}{
		"id": guestID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not remove guest", err)
		return
	}

	// Update guest count (fire-and-forget)
	go updateVIPListGuestCountInternal(venueID, vipListID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Guest removed successfully",
	})
}

// ConfirmGuestAttendance confirms a guest's attendance
// POST /api/v1/vip-lists/guests/:guest_id/confirm
func ConfirmGuestAttendance(c *gin.Context) {
	guestID := c.Param("guest_id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// First get guest from central DB to find VIP list and venue
	centralClient := services.DB.Default()
	guest, err := centralClient.QueryOne(ctx, "vip_list_guests", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id": guestID,
		},
	})

	if err != nil || guest == nil {
		middleware.SafeError(c, http.StatusNotFound, "Guest not found", err)
		return
	}

	// Get VIP list to find venue_id
	vipListID := services.GetString(guest, "vip_list_reservation_id")
	vipList, _ := centralClient.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "venue_id,price_per_person,organizer_pays",
		"where":  map[string]interface{}{"id": vipListID},
	})

	if vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", nil)
		return
	}

	// Route to venue-specific DB
	venueID := services.GetString(vipList, "venue_id")
	venueClient := services.DB.ForVenue(venueID)
	if venueClient == nil {
		venueClient = centralClient // Fallback
	}

	status := services.GetString(guest, "status")
	if status != string(models.ReservationStatusPending) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Guest has already responded"})
		return
	}

	// Update in venue-specific DB
	_, err = venueClient.UpdateCtx(ctx, "vip_list_guests", map[string]interface{}{
		"status":       string(models.ReservationStatusConfirmed),
		"confirmed_at": time.Now().Format(time.RFC3339),
		"updated_at":   time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": guestID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not confirm attendance", err)
		return
	}

	requiresPayment := false
	if vipList != nil {
		pricePerPerson := services.GetFloat64(vipList, "price_per_person")
		organizerPays := services.GetBool(vipList, "organizer_pays")
		requiresPayment = pricePerPerson > 0 && !organizerPays
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Attendance confirmed",
		"data": gin.H{
			"qr_token":         services.GetString(guest, "qr_token"),
			"requires_payment": requiresPayment,
		},
	})
}

// =============================================
// STAFF ENDPOINTS
// =============================================

// GetEventVIPLists returns all VIP lists for an event
// GET /api/v1/staff/events/:id/vip-lists
func GetEventVIPLists(c *gin.Context) {
	eventID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	vipLists, err := client.QueryCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"event_id": eventID,
			"venue_id": staffClaims.VenueID,
		},
		"order": "created_at.desc",
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load VIP lists", err)
		return
	}

	// =============================================
	// OPTIMIZED: Single query for all guests (eliminates N+1)
	// =============================================
	var stats struct {
		Total           int `json:"total"`
		Pending         int `json:"pending"`
		Confirmed       int `json:"confirmed"`
		TotalGuests     int `json:"total_guests"`
		ConfirmedGuests int `json:"confirmed_guests"`
		CheckedIn       int `json:"checked_in"`
	}

	stats.Total = len(vipLists)

	// Collect all VIP list IDs for batch query
	vipListIDs := make([]string, 0, len(vipLists))
	for _, vl := range vipLists {
		vipListIDs = append(vipListIDs, services.GetString(vl, "id"))
	}

	// Single batch query for all guests (instead of N queries)
	var allGuests []map[string]interface{}
	if len(vipListIDs) > 0 {
		allGuests, _ = client.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
			"select": "*",
			"where": map[string]interface{}{
				"vip_list_reservation_id": services.FormatInClause(vipListIDs),
			},
		})
	}

	// Build map: vipListID -> guests for O(1) lookup
	guestsByVIPList := make(map[string][]map[string]interface{}, len(vipLists))
	for _, g := range allGuests {
		vlID := services.GetString(g, "vip_list_reservation_id")
		guestsByVIPList[vlID] = append(guestsByVIPList[vlID], g)

		// Calculate guest stats in the same pass
		guestStatus := services.GetString(g, "status")
		if guestStatus == "confirmed" || guestStatus == "paid" {
			stats.ConfirmedGuests++
		}
		if services.GetBool(g, "checked_in") {
			stats.CheckedIn++
		}
	}
	stats.TotalGuests = len(allGuests)

	// Attach guests to VIP lists and calculate VIP list stats
	for i, vl := range vipLists {
		vipListID := services.GetString(vl, "id")
		status := services.GetString(vl, "status")

		switch status {
		case "pending":
			stats.Pending++
		case "confirmed", "paid":
			stats.Confirmed++
		}

		// O(1) lookup instead of database query
		vipLists[i]["guests"] = guestsByVIPList[vipListID]
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    vipLists,
		"stats":   stats,
	})
}

// ApproveVIPList approves a VIP list reservation
// POST /api/v1/staff/vip-lists/:id/approve
func ApproveVIPList(c *gin.Context) {
	vipListID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get VIP list
	vipList, err := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id":       vipListID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	status := services.GetString(vipList, "status")
	if status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VIP list is not pending approval"})
		return
	}

	_, err = client.UpdateCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"status":      string(models.ReservationStatusConfirmed),
		"approved_by": staffClaims.UserID,
		"approved_at": time.Now().Format(time.RFC3339),
		"updated_at":  time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": vipListID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not approve VIP list", err)
		return
	}

	// Send approval email
	organizerEmail := services.GetString(vipList, "organizer_email")
	organizerName := services.GetString(vipList, "organizer_name")
	listName := services.GetString(vipList, "list_name")
	go services.Email.SendVIPListApproved(organizerEmail, organizerName, listName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "VIP list approved",
	})
}

// RejectVIPList rejects a VIP list reservation
// POST /api/v1/staff/vip-lists/:id/reject
func RejectVIPList(c *gin.Context) {
	vipListID := c.Param("id")

	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	vipList, err := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id":       vipListID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	updateData := map[string]interface{}{
		"status":      string(models.ReservationStatusCancelled),
		"rejected_by": staffClaims.UserID,
		"rejected_at": time.Now().Format(time.RFC3339),
		"updated_at":  time.Now().Format(time.RFC3339),
	}

	if req.Reason != "" {
		updateData["reject_reason"] = req.Reason
	}

	_, err = client.UpdateCtx(ctx, "vip_list_reservations", updateData, map[string]interface{}{
		"id": vipListID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not reject VIP list", err)
		return
	}

	// Send rejection email
	organizerEmail := services.GetString(vipList, "organizer_email")
	organizerName := services.GetString(vipList, "organizer_name")
	listName := services.GetString(vipList, "list_name")
	go services.Email.SendVIPListRejected(organizerEmail, organizerName, listName, req.Reason)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "VIP list rejected",
	})
}

// CheckInVIPGuest checks in a VIP list guest
// POST /api/v1/staff/vip-lists/check-in
func CheckInVIPGuest(c *gin.Context) {
	var req struct {
		QRToken string `json:"qr_token" binding:"required"`
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

	// Find guest by QR token
	guest, err := client.QueryOne(ctx, "vip_list_guests", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"qr_token": req.QRToken,
		},
	})

	if err != nil || guest == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Guest not found",
		})
		return
	}

	// Check if already checked in
	if services.GetBool(guest, "checked_in") {
		checkedInAt := services.GetString(guest, "checked_in_at")
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Guest already checked in",
			"data": gin.H{
				"guest_name":    services.GetString(guest, "name"),
				"checked_in_at": checkedInAt,
			},
		})
		return
	}

	// Get VIP list to check status
	vipListID := services.GetString(guest, "vip_list_reservation_id")
	vipList, _ := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "status,list_name,price_per_person,organizer_pays",
		"where":  map[string]interface{}{"id": vipListID},
	})

	if vipList != nil {
		vipListStatus := services.GetString(vipList, "status")
		if vipListStatus == "cancelled" {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "VIP list has been cancelled",
			})
			return
		}

		// Check payment if required
		pricePerPerson := services.GetFloat64(vipList, "price_per_person")
		organizerPays := services.GetBool(vipList, "organizer_pays")
		amountPaid := services.GetFloat64(guest, "amount_paid")
		amountToPay := services.GetFloat64(guest, "amount_to_pay")

		if pricePerPerson > 0 && !organizerPays && amountPaid < amountToPay {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "Payment required",
				"data": gin.H{
					"amount_due": amountToPay - amountPaid,
				},
			})
			return
		}
	}

	// Perform check-in
	guestID := services.GetString(guest, "id")
	_, err = client.UpdateCtx(ctx, "vip_list_guests", map[string]interface{}{
		"checked_in":    true,
		"checked_in_at": time.Now().Format(time.RFC3339),
		"checked_in_by": staffClaims.UserID,
		"updated_at":    time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": guestID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not check in guest", err)
		return
	}

	listName := ""
	if vipList != nil {
		listName = services.GetString(vipList, "list_name")
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Check-in successful",
		"data": gin.H{
			"guest_name":   services.GetString(guest, "name"),
			"list_name":    listName,
			"is_organizer": services.GetBool(guest, "is_organizer"),
		},
	})
}

// UndoVIPGuestCheckIn reverts a check-in
// POST /api/v1/staff/vip-lists/guests/:id/undo-checkin
func UndoVIPGuestCheckIn(c *gin.Context) {
	guestID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	_, err := client.UpdateCtx(ctx, "vip_list_guests", map[string]interface{}{
		"checked_in":    false,
		"checked_in_at": nil,
		"checked_in_by": nil,
		"updated_at":    time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": guestID,
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

// =============================================
// STAFF VIP LIST MANAGEMENT - MOBILE APP ENDPOINTS
// =============================================

// GetVenueVIPLists returns all VIP lists for a venue with filters and pagination
// GET /api/v1/vip-lists/venue/:venueId
func GetVenueVIPLists(c *gin.Context) {
	venueID := c.Param("venueId")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	// Verify staff belongs to this venue
	if staffClaims.VenueID != venueID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Parse query parameters
	status := c.Query("status")
	eventID := c.Query("event_id")
	search := c.Query("search")
	page := services.GetIntParam(c, "page", 1)
	limit := services.GetIntParam(c, "limit", 20)
	offset := (page - 1) * limit

	client := services.DB.ForVenue(venueID)

	// Build query filters
	whereClause := map[string]interface{}{
		"venue_id": venueID,
	}

	if status != "" && status != "all" {
		whereClause["status"] = status
	}
	if eventID != "" {
		whereClause["event_id"] = eventID
	}

	// Get total count for pagination
	allResults, err := client.QueryCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "id",
		"where":  whereClause,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not count VIP lists", err)
		return
	}
	totalCount := len(allResults)

	// Get paginated results
	queryParams := map[string]interface{}{
		"select": "*",
		"where":  whereClause,
		"order":  "created_at.desc",
		"limit":  limit,
		"offset": offset,
	}

	vipLists, err := client.QueryCtx(ctx, "vip_list_reservations", queryParams)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not load VIP lists", err)
		return
	}

	// Filter by search term if provided (host name or email)
	if search != "" {
		searchLower := strings.ToLower(search)
		filtered := make([]map[string]interface{}, 0)
		for _, vl := range vipLists {
			hostName := strings.ToLower(services.GetString(vl, "host_name") + " " + services.GetString(vl, "host_last_name"))
			hostEmail := strings.ToLower(services.GetString(vl, "host_email"))
			reservationName := strings.ToLower(services.GetString(vl, "reservation_name"))
			if strings.Contains(hostName, searchLower) || strings.Contains(hostEmail, searchLower) || strings.Contains(reservationName, searchLower) {
				filtered = append(filtered, vl)
			}
		}
		vipLists = filtered
	}

	// Batch fetch guests for all VIP lists
	vipListIDs := make([]string, 0, len(vipLists))
	for _, vl := range vipLists {
		vipListIDs = append(vipListIDs, services.GetString(vl, "id"))
	}

	var allGuests []map[string]interface{}
	if len(vipListIDs) > 0 {
		allGuests, _ = client.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
			"select": "id,vip_list_reservation_id,name,gender,status,amount_paid,amount_to_pay,checked_in",
			"where": map[string]interface{}{
				"vip_list_reservation_id": services.FormatInClause(vipListIDs),
			},
		})
	}

	// Build guest map for O(1) lookup
	guestsByVIPList := make(map[string][]map[string]interface{})
	guestStats := make(map[string]map[string]int)
	for _, g := range allGuests {
		vlID := services.GetString(g, "vip_list_reservation_id")
		guestsByVIPList[vlID] = append(guestsByVIPList[vlID], g)

		// Calculate stats per VIP list
		if guestStats[vlID] == nil {
			guestStats[vlID] = map[string]int{"total": 0, "confirmed": 0, "paid": 0, "checked_in": 0, "men": 0, "women": 0}
		}
		guestStats[vlID]["total"]++
		status := services.GetString(g, "status")
		if status == "confirmed" || status == "paid" {
			guestStats[vlID]["confirmed"]++
		}
		if services.GetFloat64(g, "amount_paid") >= services.GetFloat64(g, "amount_to_pay") {
			guestStats[vlID]["paid"]++
		}
		if services.GetBool(g, "checked_in") {
			guestStats[vlID]["checked_in"]++
		}
		gender := services.GetString(g, "gender")
		if gender == "male" {
			guestStats[vlID]["men"]++
		} else if gender == "female" {
			guestStats[vlID]["women"]++
		}
	}

	// Batch fetch events for all VIP lists
	eventIDs := make([]string, 0)
	eventIDSet := make(map[string]bool)
	for _, vl := range vipLists {
		eid := services.GetString(vl, "event_id")
		if eid != "" && !eventIDSet[eid] {
			eventIDs = append(eventIDs, eid)
			eventIDSet[eid] = true
		}
	}

	eventsMap := make(map[string]map[string]interface{})
	if len(eventIDs) > 0 {
		events, _ := client.QueryCtx(ctx, "events", map[string]interface{}{
			"select": "id,name,event_date,start_time,image",
			"where": map[string]interface{}{
				"id": services.FormatInClause(eventIDs),
			},
		})
		for _, e := range events {
			eventsMap[services.GetString(e, "id")] = e
		}
	}

	// Enrich VIP lists with guests, stats, and event info
	for i, vl := range vipLists {
		vlID := services.GetString(vl, "id")
		eventID := services.GetString(vl, "event_id")

		vipLists[i]["guests"] = guestsByVIPList[vlID]
		vipLists[i]["guest_stats"] = guestStats[vlID]
		if event, ok := eventsMap[eventID]; ok {
			vipLists[i]["event"] = event
		}
	}

	// Calculate overall stats
	var stats struct {
		Total     int `json:"total"`
		Open      int `json:"open"`
		Closed    int `json:"closed"`
		Completed int `json:"completed"`
		Cancelled int `json:"cancelled"`
	}
	stats.Total = totalCount
	for _, vl := range vipLists {
		switch services.GetString(vl, "status") {
		case "open":
			stats.Open++
		case "closed":
			stats.Closed++
		case "completed":
			stats.Completed++
		case "cancelled":
			stats.Cancelled++
		}
	}

	totalPages := (totalCount + limit - 1) / limit

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    vipLists,
		"stats":   stats,
		"pagination": gin.H{
			"current_page": page,
			"total_pages":  totalPages,
			"total_count":  totalCount,
			"has_more":     page < totalPages,
			"limit":        limit,
		},
	})
}

// GetVIPListDetail returns detailed info for a specific VIP list
// GET /api/v1/vip-lists/detail/:id
func GetVIPListDetail(c *gin.Context) {
	vipListID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get VIP list
	vipList, err := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id":       vipListID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	// Get guests with full details
	guests, _ := client.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"vip_list_reservation_id": vipListID,
		},
		"order": "is_host.desc,created_at.asc",
	})

	// Get event info
	eventID := services.GetString(vipList, "event_id")
	event, _ := client.QueryOne(ctx, "events", map[string]interface{}{
		"select": "id,name,event_date,start_time,end_time,image,venue_id",
		"where": map[string]interface{}{
			"id": eventID,
		},
	})

	// Calculate stats
	stats := map[string]interface{}{
		"total_guests":     len(guests),
		"confirmed_guests": 0,
		"paid_guests":      0,
		"checked_in":       0,
		"men_count":        0,
		"women_count":      0,
		"total_collected":  0.0,
		"total_expected":   0.0,
	}

	for _, g := range guests {
		status := services.GetString(g, "status")
		if status == "confirmed" || status == "paid" {
			stats["confirmed_guests"] = stats["confirmed_guests"].(int) + 1
		}
		amountPaid := services.GetFloat64(g, "amount_paid")
		amountToPay := services.GetFloat64(g, "amount_to_pay")
		if amountPaid >= amountToPay && amountToPay > 0 {
			stats["paid_guests"] = stats["paid_guests"].(int) + 1
		}
		if services.GetBool(g, "checked_in") {
			stats["checked_in"] = stats["checked_in"].(int) + 1
		}
		gender := services.GetString(g, "gender")
		if gender == "male" {
			stats["men_count"] = stats["men_count"].(int) + 1
		} else if gender == "female" {
			stats["women_count"] = stats["women_count"].(int) + 1
		}
		stats["total_collected"] = stats["total_collected"].(float64) + amountPaid
		stats["total_expected"] = stats["total_expected"].(float64) + amountToPay
	}

	vipList["guests"] = guests
	vipList["event"] = event
	vipList["stats"] = stats

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    vipList,
	})
}

// CloseVIPList closes a VIP list and sets a payment deadline
// POST /api/v1/vip-lists/:id/close
func CloseVIPList(c *gin.Context) {
	vipListID := c.Param("id")

	var req struct {
		DeadlineHours int `json:"deadline_hours"` // Hours from now until payment deadline
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// Default to 48 hours if not specified
		req.DeadlineHours = 48
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get VIP list
	vipList, err := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id":       vipListID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	currentStatus := services.GetString(vipList, "status")
	if currentStatus != "open" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VIP list must be in 'open' status to close"})
		return
	}

	// Calculate payment deadline
	paymentDeadline := time.Now().Add(time.Duration(req.DeadlineHours) * time.Hour)

	// Update VIP list status
	_, err = client.UpdateCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"status":           "closed",
		"closed_at":        time.Now().Format(time.RFC3339),
		"closed_by":        staffClaims.UserID,
		"payment_deadline": paymentDeadline.Format(time.RFC3339),
		"updated_at":       time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": vipListID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not close VIP list", err)
		return
	}

	// Get confirmed guests to notify
	guests, _ := client.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "id,name,email,phone,phone_prefix,status,amount_to_pay,amount_paid",
		"where": map[string]interface{}{
			"vip_list_reservation_id": vipListID,
			"status":                  "confirmed",
		},
	})

	// Send payment deadline notifications (fire-and-forget)
	hostName := services.GetString(vipList, "host_name")
	reservationName := services.GetString(vipList, "reservation_name")
	go sendPaymentDeadlineNotifications(staffClaims.VenueID, vipListID, hostName, reservationName, paymentDeadline, guests)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "VIP list closed successfully",
		"data": gin.H{
			"status":           "closed",
			"payment_deadline": paymentDeadline.Format(time.RFC3339),
			"deadline_hours":   req.DeadlineHours,
			"guests_to_pay":    len(guests),
		},
	})
}

// FinalizeVIPList finalizes a VIP list after payment period ends
// POST /api/v1/vip-lists/:id/finalize
func FinalizeVIPList(c *gin.Context) {
	vipListID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get VIP list
	vipList, err := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id":       vipListID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	currentStatus := services.GetString(vipList, "status")
	if currentStatus != "closed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VIP list must be in 'closed' status to finalize"})
		return
	}

	// Calculate total collected and budget for bottles
	guests, _ := client.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "amount_paid,status",
		"where": map[string]interface{}{
			"vip_list_reservation_id": vipListID,
		},
	})

	var totalCollected float64
	var paidGuestsCount int
	for _, g := range guests {
		paid := services.GetFloat64(g, "amount_paid")
		totalCollected += paid
		if paid > 0 {
			paidGuestsCount++
		}
	}

	// Generate bottle selection token/URL
	bottleSelectionToken := generateSecureToken()
	bottleSelectionURL := fmt.Sprintf("https://web.pullevents.com/es/vip/bottles/%s", bottleSelectionToken)

	// Update VIP list status
	_, err = client.UpdateCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"status":                 "completed",
		"finalized_at":           time.Now().Format(time.RFC3339),
		"finalized_by":           staffClaims.UserID,
		"total_collected":        totalCollected,
		"bottle_budget":          totalCollected, // Budget for bottles equals collected amount
		"bottle_selection_token": bottleSelectionToken,
		"updated_at":             time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": vipListID,
	})

	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not finalize VIP list", err)
		return
	}

	// Send bottle selection notification to host (fire-and-forget)
	hostEmail := services.GetString(vipList, "host_email")
	hostName := services.GetString(vipList, "host_name")
	hostPhone := services.GetString(vipList, "host_phone")
	hostPhonePrefix := services.GetString(vipList, "host_phone_prefix")
	go sendBottleSelectionNotification(hostEmail, hostName, hostPhone, hostPhonePrefix, bottleSelectionURL, totalCollected)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "VIP list finalized successfully",
		"data": gin.H{
			"status":               "completed",
			"total_collected":      totalCollected,
			"bottle_budget":        totalCollected,
			"paid_guests":          paidGuestsCount,
			"bottle_selection_url": bottleSelectionURL,
		},
	})
}

// GetVIPListBottles returns the bottles selected for a VIP list
// GET /api/v1/vip-lists/:id/bottles
func GetVIPListBottles(c *gin.Context) {
	vipListID := c.Param("id")

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get VIP list
	vipList, err := client.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "id,bottle_budget,total_collected,status",
		"where": map[string]interface{}{
			"id":       vipListID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || vipList == nil {
		middleware.SafeError(c, http.StatusNotFound, "VIP list not found", err)
		return
	}

	// Get selected bottles
	bottles, _ := client.QueryCtx(ctx, "vip_list_bottles", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"vip_list_reservation_id": vipListID,
		},
		"order": "created_at.asc",
	})

	// Calculate totals
	var totalValue float64
	for _, b := range bottles {
		totalValue += services.GetFloat64(b, "price") * float64(services.GetInt(b, "quantity"))
	}

	budget := services.GetFloat64(vipList, "bottle_budget")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"bottles":          bottles,
			"total_value":      totalValue,
			"budget":           budget,
			"remaining_credit": budget - totalValue,
		},
	})
}

// CreateVIPListStaff creates a new VIP list (staff version with full host info)
// POST /api/v1/vip-lists/create
func CreateVIPListStaff(c *gin.Context) {
	var req struct {
		EventID         string `json:"event_id" binding:"required"`
		TableOrBar      string `json:"table_or_bar"` // "table" or "bar"
		HostName        string `json:"host_name" binding:"required"`
		HostLastName    string `json:"host_last_name"`
		HostEmail       string `json:"host_email" binding:"required,email"`
		HostPhone       string `json:"host_phone"`
		HostPhonePrefix string `json:"host_phone_prefix"`
		HostBirthDate   string `json:"host_birth_date"`
		HostGender      string `json:"host_gender"` // "male" or "female"
		ExpectedMen     int    `json:"expected_men"`
		ExpectedWomen   int    `json:"expected_women"`
		ReservationName string `json:"reservation_name"`
		Description     string `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.SafeError(c, http.StatusBadRequest, "Invalid request format", err)
		return
	}

	claims, _ := c.Get("staff")
	staffClaims := claims.(*models.StaffClaims)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	client := services.DB.ForVenue(staffClaims.VenueID)

	// Get event to validate
	event, err := client.QueryOne(ctx, "events", map[string]interface{}{
		"select": "id,name,event_date,venue_id,organization_id",
		"where": map[string]interface{}{
			"id":       req.EventID,
			"venue_id": staffClaims.VenueID,
		},
	})

	if err != nil || event == nil {
		middleware.SafeError(c, http.StatusNotFound, "Event not found", err)
		return
	}

	// Get VIP pricing for the event (gender-based if configured)
	pricing, _ := client.QueryOne(ctx, "event_vip_ticket_types", map[string]interface{}{
		"select": "male_price,female_price,price_per_person",
		"where": map[string]interface{}{
			"event_id": req.EventID,
		},
	})

	malePrice := 0.0
	femalePrice := 0.0
	if pricing != nil {
		malePrice = services.GetFloat64(pricing, "male_price")
		femalePrice = services.GetFloat64(pricing, "female_price")
		if malePrice == 0 && femalePrice == 0 {
			// Fall back to generic price
			genericPrice := services.GetFloat64(pricing, "price_per_person")
			malePrice = genericPrice
			femalePrice = genericPrice
		}
	}

	// Generate tracking link code
	trackingLinkCode := generateSecureToken()[:12]
	trackingURL := fmt.Sprintf("https://web.pullevents.com/es/vip/track/%s", trackingLinkCode)

	// Create VIP list reservation
	now := time.Now().Format(time.RFC3339)
	vipListData := map[string]interface{}{
		"event_id":           req.EventID,
		"venue_id":           staffClaims.VenueID,
		"organization_id":    services.GetString(event, "organization_id"),
		"created_by_staff":   staffClaims.UserID,
		"status":             "open",
		"table_or_bar":       req.TableOrBar,
		"host_name":          middleware.SanitizeName(req.HostName),
		"host_last_name":     middleware.SanitizeName(req.HostLastName),
		"host_email":         middleware.SanitizeEmail(req.HostEmail),
		"host_phone":         req.HostPhone,
		"host_phone_prefix":  req.HostPhonePrefix,
		"host_birth_date":    req.HostBirthDate,
		"host_gender":        req.HostGender,
		"expected_men":       req.ExpectedMen,
		"expected_women":     req.ExpectedWomen,
		"reservation_name":   req.ReservationName,
		"description":        req.Description,
		"male_price":         malePrice,
		"female_price":       femalePrice,
		"tracking_link_code": trackingLinkCode,
		"current_guests":     0,
		"total_collected":    0,
		"created_at":         now,
		"updated_at":         now,
	}

	result, err := client.InsertCtx(ctx, "vip_list_reservations", vipListData)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Could not create VIP list", err)
		return
	}

	reservationID := services.GetString(result, "id")

	// Add host as first guest
	hostGuestData := map[string]interface{}{
		"vip_list_reservation_id": reservationID,
		"name":                    req.HostName + " " + req.HostLastName,
		"email":                   req.HostEmail,
		"phone":                   req.HostPhone,
		"phone_prefix":            req.HostPhonePrefix,
		"gender":                  req.HostGender,
		"birth_date":              req.HostBirthDate,
		"is_host":                 true,
		"status":                  "confirmed",
		"qr_token":                generateSecureToken(),
		"amount_to_pay":           0, // Host doesn't pay
		"amount_paid":             0,
		"checked_in":              false,
		"created_at":              now,
		"updated_at":              now,
	}
	client.InsertCtx(ctx, "vip_list_guests", hostGuestData)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "VIP list created successfully",
		"data": gin.H{
			"reservation_id":     reservationID,
			"tracking_url":       trackingURL,
			"tracking_link_code": trackingLinkCode,
			"status":             "open",
			"male_price":         malePrice,
			"female_price":       femalePrice,
		},
	})
}

// =============================================
// NOTIFICATION HELPERS
// =============================================

func sendPaymentDeadlineNotifications(venueID, vipListID, hostName, reservationName string, deadline time.Time, guests []map[string]interface{}) {
	// This would integrate with WhatsApp/Email services
	// For now, just log
	for _, g := range guests {
		amountToPay := services.GetFloat64(g, "amount_to_pay")
		amountPaid := services.GetFloat64(g, "amount_paid")
		if amountPaid < amountToPay {
			guestName := services.GetString(g, "name")
			fmt.Printf("[VIP List] Payment reminder: %s owes %.2f for %s's VIP list '%s'. Deadline: %s\n",
				guestName, amountToPay-amountPaid, hostName, reservationName, deadline.Format("Jan 2, 3:04 PM"))
		}
	}
}

func sendBottleSelectionNotification(email, name, phone, phonePrefix, bottleURL string, budget float64) {
	// This would integrate with WhatsApp/Email services
	fmt.Printf("[VIP List] Bottle selection ready for %s (%s). Budget: %.2f. URL: %s\n",
		name, email, budget, bottleURL)

	// TODO: Send WhatsApp message
	// message := fmt.Sprintf("🍾 ¡Felicidades %s! Tu VIP List ha sido finalizada.\n\nTienes un presupuesto de Q%.2f para seleccionar tus botellas.\n\n👉 %s",
	// 	name, budget, bottleURL)
	// services.WhatsApp.Send(phonePrefix+phone, message)
}

// =============================================
// HELPER FUNCTIONS
// =============================================

func generateSecureToken() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func getEventByIDInternal(ctx context.Context, eventID string) (*models.Event, error) {
	client := services.DB.Default()

	result, err := client.QueryOne(ctx, "events", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"id": eventID,
		},
	})

	if err != nil || result == nil {
		return nil, fmt.Errorf("event not found")
	}

	return &models.Event{
		ID:             services.GetString(result, "id"),
		VenueID:        services.GetString(result, "venue_id"),
		OrganizationID: services.GetString(result, "organization_id"),
		Name:           services.GetString(result, "name"),
		Slug:           services.GetString(result, "slug"),
		EventDate:      services.GetString(result, "event_date"),
		StartTime:      services.GetString(result, "start_time"),
		EndTime:        services.GetString(result, "end_time"),
	}, nil
}

func getVIPListPricingInternal(ctx context.Context, venueID, eventID string) float64 {
	client := services.DB.ForVenue(venueID)

	result, err := client.QueryOne(ctx, "vip_list_pricing", map[string]interface{}{
		"select": "price_per_person",
		"where": map[string]interface{}{
			"event_id": eventID,
		},
	})

	if err != nil || result == nil {
		return 0
	}

	return services.GetFloat64(result, "price_per_person")
}

func addVIPListGuestsInternal(venueID, vipListID string, guests []models.CreateVIPListGuestInput, pricePerPerson float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)
	now := time.Now().Format(time.RFC3339)

	// OPTIMIZED: Batch insert instead of N individual inserts
	guestData := make([]map[string]interface{}, 0, len(guests))
	for _, g := range guests {
		guestData = append(guestData, map[string]interface{}{
			"vip_list_reservation_id": vipListID,
			"name":                    middleware.SanitizeName(g.Name),
			"email":                   middleware.SanitizeEmail(g.Email),
			"phone":                   g.Phone,
			"gender":                  g.Gender,
			"is_organizer":            false,
			"status":                  string(models.ReservationStatusPending),
			"qr_token":                generateSecureToken(),
			"amount_to_pay":           pricePerPerson,
			"amount_paid":             0,
			"checked_in":              false,
			"created_at":              now,
			"updated_at":              now,
		})
	}

	// Try batch insert first
	if err := client.InsertBatch(ctx, "vip_list_guests", guestData); err != nil {
		// Fallback to individual inserts if batch fails
		for _, data := range guestData {
			client.InsertCtx(ctx, "vip_list_guests", data)
		}
	}

	updateVIPListGuestCountInternal(venueID, vipListID)
}

func updateVIPListGuestCountInternal(venueID, vipListID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Count guests
	guests, _ := client.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "id",
		"where": map[string]interface{}{
			"vip_list_reservation_id": vipListID,
		},
	})

	count := len(guests)

	client.UpdateNoReturn(ctx, "vip_list_reservations", map[string]interface{}{
		"current_guests": count,
		"updated_at":     time.Now().Format(time.RFC3339),
	}, map[string]interface{}{
		"id": vipListID,
	})
}

func createVIPListNotificationInternal(venueID, orgID, vipListID, organizerName, eventName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	notificationData := map[string]interface{}{
		"venue_id":        venueID,
		"organization_id": orgID,
		"type":            string(models.NotifyNewVIPList),
		"priority":        string(models.PriorityNormal),
		"title":           "Nueva VIP List",
		"message":         fmt.Sprintf("%s ha creado una VIP list para %s", organizerName, eventName),
		"target_type":     "staff",
		"target_role":     "admin",
		"vip_list_id":     vipListID,
		"is_read":         false,
		"created_at":      time.Now().Format(time.RFC3339),
	}

	client.InsertCtx(ctx, "notifications", notificationData)
}

func stringPtr(s string) *string {
	return &s
}

// Helper for lowercase comparison
func equalFoldEmail(a, b string) bool {
	return strings.EqualFold(a, b)
}
