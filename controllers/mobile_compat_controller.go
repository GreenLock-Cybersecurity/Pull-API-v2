package controllers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"pull-api-v2/services"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================
// MOBILE APP COMPAT LAYER
// =============================================
//
// PullMobileApp-GL was built against Pull-API-Go (v1). It expects path
// shapes like:
//
//   GET  /event/upcoming-events/:venueId
//   GET  /event/get-event-details/:eventId
//   POST /ticket-validation/validate-ticket
//   GET  /orders/venue/:venueId
//   POST /orders/:orderId/approve
//   POST /orders/:orderId/reject
//   GET  /group-reservations/details/:reservationId
//   POST /group-reservations/:id/approve
//   POST /group-reservations/:id/reject
//   GET  /venue/get-venue-info/:venueId
//   POST /notifications/register-token
//   POST /notifications/unregister-token
//
// We adapt these to the v2 lookup logic + multi-tenant DatabaseRouter.

// MobileGetUpcomingEvents handles GET /event/upcoming-events/:venueId
func MobileGetUpcomingEvents(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.Param("venueId")

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	events, err := venueDB.QueryCtx(ctx, "events", map[string]interface{}{
		"select": services.EventSelectColumns,
		"where": map[string]interface{}{
			"status":         services.PublishedEventStatuses,
			"deleted_at":     "is.null",
			"start_datetime": "gte." + time.Now().Format(time.RFC3339),
		},
		"order": "start_datetime.asc",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get events"})
		return
	}
	services.EnrichEvents(events)
	for i := range events {
		events[i]["venue_id"] = venueID
	}
	// PullMobileApp-GL expects a bare array — eventService maps response.data
	// directly to its store. If we returned {events: [...]} the store would
	// receive an object and silently render no events.
	c.JSON(http.StatusOK, events)
}

// MobileCreateEventWithTickets handles POST /event/create-event-with-tickets.
// Body matches the EventoNuevo screen — name/description/event_date/start_time/
// end_time/ticket_limit/dress_code/min_age/custom_location/ticket_types[].
func MobileCreateEventWithTickets(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id missing in token"})
		return
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	startDT, endDT := combineDateTime(
		services.GetString(body, "event_date"),
		services.GetString(body, "start_time"),
		services.GetString(body, "end_time"),
	)
	if startDT == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_date and start_time required"})
		return
	}
	name := services.GetString(body, "name")
	slug := slugify(name) + "-" + time.Now().Format("20060102150405")

	// organization_id is required for inserts in this schema. Pull it from JWT
	// claims, falling back to the venue row if not present.
	orgID := c.GetString("organization_id")
	if orgID == "" {
		v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
			"select": "organization_id", "where": map[string]interface{}{"id": venueID},
		})
		if v != nil {
			orgID = services.GetString(v, "organization_id")
		}
	}

	insertPayload := map[string]interface{}{
		"venue_id":        venueID,
		"organization_id": orgID,
		"name":            name,
		"description":     services.GetString(body, "description"),
		"slug":            slug,
		"start_datetime":  startDT,
		"end_datetime":    endDT,
		"status":          "draft",
		"image":           services.GetString(body, "image"),
		"dress_code":      services.GetString(body, "dress_code"),
		"min_age":         services.GetInt(body, "min_age"),
		"custom_location": services.GetString(body, "custom_location"),
		"capacity":        services.GetInt(body, "ticket_limit"),
	}
	if tableCap, ok := body["table_capacity"]; ok {
		insertPayload["table_capacity"] = tableCap
	}

	created, err := venueDB.InsertCtx(ctx, "events", insertPayload)
	if err != nil || created == nil {
		log.Printf("[Mobile/CreateEvent] insert err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create event", "details": fmt.Sprintf("%v", err)})
		return
	}
	eventID := services.GetString(created, "id")

	// Insert ticket types if provided.
	tts, _ := body["ticket_types"].([]interface{})
	createdTickets := []map[string]interface{}{}
	for _, t := range tts {
		tt, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		var price float64
		switch v := tt["price"].(type) {
		case float64:
			price = v
		case int:
			price = float64(v)
		case string:
			var p float64
			fmt.Sscanf(v, "%f", &p)
			price = p
		}
		ttPayload := map[string]interface{}{
			"event_id":  eventID,
			"name":      services.GetString(tt, "name"),
			"price":     price,
			"quantity":  services.GetInt(tt, "quantity"),
			"benefits":  services.GetString(tt, "benefits"),
			"is_active": true,
		}
		row, terr := venueDB.InsertCtx(ctx, "ticket_types", ttPayload)
		if terr == nil && row != nil {
			createdTickets = append(createdTickets, row)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"event_id":     eventID,
		"event":        created,
		"ticket_types": createdTickets,
	})
}

// MobileCreateEvent handles POST /event/create-event (no ticket types).
func MobileCreateEvent(c *gin.Context) {
	// Same impl as CreateEventWithTickets but ignores ticket_types.
	MobileCreateEventWithTickets(c)
}

// MobileUpdateEvent handles PUT /event/update-event/:eventId.
func MobileUpdateEvent(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	updates := map[string]interface{}{}
	for _, k := range []string{"name", "description", "image", "dress_code", "custom_location", "status", "deleted_at"} {
		if v, ok := body[k]; ok {
			updates[k] = v
		}
	}
	if v, ok := body["min_age"]; ok {
		updates["min_age"] = v
	}
	if v, ok := body["ticket_limit"]; ok {
		updates["capacity"] = v
	}
	if v, ok := body["table_capacity"]; ok {
		updates["table_capacity"] = v
	}
	startDT, endDT := combineDateTime(
		services.GetString(body, "event_date"),
		services.GetString(body, "start_time"),
		services.GetString(body, "end_time"),
	)
	if startDT != "" {
		updates["start_datetime"] = startDT
	}
	if endDT != "" {
		updates["end_datetime"] = endDT
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no updates supplied"})
		return
	}
	result, err := venueDB.UpdateCtx(ctx, "events", updates, map[string]interface{}{
		"id": c.Param("eventId"),
	})
	if err != nil || len(result) == 0 {
		log.Printf("[Mobile/UpdateEvent] err=%v rows=%d", err, len(result))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update event"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "event": result[0]})
}

// MobileDeleteEvent handles DELETE /event/delete-event/:eventId (soft delete).
func MobileDeleteEvent(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	now := time.Now().Format(time.RFC3339)
	result, err := venueDB.UpdateCtx(ctx, "events", map[string]interface{}{
		"deleted_at": now,
		"status":     "cancelled",
	}, map[string]interface{}{"id": c.Param("eventId")})
	if err != nil || len(result) == 0 {
		log.Printf("[Mobile/DeleteEvent] err=%v rows=%d", err, len(result))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete event"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// combineDateTime takes "2026-07-03" + "22:00:00" / "03:00:00" and returns
// RFC3339 start/end datetimes. End times before start times roll over to the
// next day (e.g. doors at 22:00, closing at 03:00 the next morning).
func combineDateTime(eventDate, startTime, endTime string) (string, string) {
	if eventDate == "" || startTime == "" {
		return "", ""
	}
	// Normalise time to HH:MM:SS
	if len(startTime) == 5 {
		startTime += ":00"
	}
	if endTime != "" && len(endTime) == 5 {
		endTime += ":00"
	}
	startStr := eventDate + "T" + startTime + "-06:00"
	if endTime == "" {
		return startStr, ""
	}
	// Same date for end; if end < start hour, roll one day forward.
	endStr := eventDate + "T" + endTime + "-06:00"
	if endTime < startTime {
		t, err := time.Parse("2006-01-02", eventDate)
		if err == nil {
			endStr = t.Add(24*time.Hour).Format("2006-01-02") + "T" + endTime + "-06:00"
		}
	}
	return startStr, endStr
}

// slugify lowercases, replaces spaces with -, and strips non-alphanumeric chars.
func slugify(s string) string {
	out := strings.Builder{}
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			out.WriteRune('-')
		}
	}
	res := out.String()
	if res == "" {
		res = "event"
	}
	return res
}

// MobileGetEventDetails handles GET /event/get-event-details/:eventId
func MobileGetEventDetails(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	eventID := c.Param("eventId")

	// Try to find the event across the staff's venue (taken from JWT claims).
	venueID := c.GetString("venue_id")
	if venueID == "" {
		v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
			"select": "id", "where": map[string]interface{}{"is_active": true, "deleted_at": "is.null"}, "limit": 1,
		})
		if v != nil {
			venueID = services.GetString(v, "id")
		}
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	// Accept either UUID or slug.
	event, _ := venueDB.QueryOne(ctx, "events", map[string]interface{}{
		"select": services.EventSelectColumns,
		"where":  map[string]interface{}{"id": eventID, "deleted_at": "is.null"},
	})
	if event == nil {
		event, _ = venueDB.QueryOne(ctx, "events", map[string]interface{}{
			"select": services.EventSelectColumns,
			"where":  map[string]interface{}{"slug": eventID, "deleted_at": "is.null"},
		})
	}
	if event == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}
	services.EnrichEvent(event)
	event["venue_id"] = venueID
	// PullMobileApp-GL's eventService reads response.data directly, expecting
	// top-level event fields. Wrapping in {event: ...} would leave the UI
	// blank because currentEvent.name etc. would be undefined.
	c.JSON(http.StatusOK, event)
}

// MobileGetVenueInfo handles GET /venue/get-venue-info/:venueId
func MobileGetVenueInfo(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
		"select": "*",
		"where":  map[string]interface{}{"id": c.Param("venueId"), "deleted_at": "is.null"},
	})
	if v == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Venue not found"})
		return
	}
	enrichVenueForLegacyFrontend(v)
	c.JSON(http.StatusOK, v)
}

// MobileValidateTicket handles POST /ticket-validation/validate-ticket
// Body: { qr_token, venue_id, organization_id, worker_id }
func MobileValidateTicket(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	var req struct {
		QRToken        string `json:"qr_token"`
		VenueID        string `json:"venue_id"`
		OrganizationID string `json:"organization_id"`
		WorkerID       string `json:"worker_id"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.QRToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "qr_token is required", "valid": false})
		return
	}
	venueID := req.VenueID
	if venueID == "" {
		venueID = c.GetString("venue_id")
	}
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id is required", "valid": false})
		return
	}

	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue", "valid": false})
		return
	}

	ticket, _ := venueDB.QueryOne(ctx, "tickets", map[string]interface{}{
		"select": "id,qr_token,event_id,order_id,ticket_type_name,owner_name,owner_email,owner_phone,is_valid,checked_in_at,checked_in_by",
		"where":  map[string]interface{}{"qr_token": req.QRToken},
	})
	if ticket == nil {
		c.JSON(http.StatusOK, gin.H{
			"valid":   false,
			"message": "Ticket no encontrado",
		})
		return
	}
	if !services.GetBool(ticket, "is_valid") {
		c.JSON(http.StatusOK, gin.H{
			"valid":   false,
			"message": "Ticket inválido",
			"ticket":  ticket,
		})
		return
	}
	if checkedAt := services.GetString(ticket, "checked_in_at"); checkedAt != "" {
		c.JSON(http.StatusOK, gin.H{
			"valid":           false,
			"already_used":    true,
			"checked_in_at":   checkedAt,
			"message":         "Ticket ya fue usado",
			"ticket":          ticket,
		})
		return
	}

	// Mark as checked in.
	ticketID := services.GetString(ticket, "id")
	venueDB.UpdateNoReturn(ctx, "tickets", map[string]interface{}{
		"checked_in_at": time.Now().Format(time.RFC3339),
		"checked_in_by": req.WorkerID,
	}, map[string]interface{}{"id": ticketID})

	// Enrich event info for the staff UI.
	var event map[string]interface{}
	if eid := services.GetString(ticket, "event_id"); eid != "" {
		event, _ = venueDB.QueryOne(ctx, "events", map[string]interface{}{
			"select": "name,start_datetime,location",
			"where":  map[string]interface{}{"id": eid},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":   true,
		"message": "Acceso permitido",
		"ticket":  ticket,
		"event":   event,
	})
}

// MobileGetVenueOrders handles GET /orders/venue/:venueId
// Query params: page, limit, status
func MobileGetVenueOrders(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.Param("venueId")
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	where := map[string]interface{}{}
	if status := c.Query("status"); status != "" && status != "All" {
		where["status"] = status
	}
	limit := 20
	if l := c.Query("limit"); l != "" {
		_, _ = fmtSscanInt(l, &limit)
	}
	page := 1
	if p := c.Query("page"); p != "" {
		_, _ = fmtSscanInt(p, &page)
	}
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	orders, err := venueDB.QueryCtx(ctx, "orders", map[string]interface{}{
		"select": "*",
		"where":  where,
		"order":  "created_at.desc",
		"limit":  limit,
		"offset": offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get orders"})
		return
	}
	// Total count for pagination — separate cheap COUNT-ish query.
	all, _ := venueDB.QueryCtx(ctx, "orders", map[string]interface{}{
		"select": "id", "where": where,
	})
	totalCount := len(all)
	totalPages := (totalCount + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	c.JSON(http.StatusOK, gin.H{
		"orders": orders,
		"pagination": gin.H{
			"currentPage": page,
			"totalPages":  totalPages,
			"totalCount":  totalCount,
			"hasMore":     page < totalPages,
			"limit":       limit,
		},
	})
}

// MobileGetOrderDetails handles GET /orders/details/:orderId
func MobileGetOrderDetails(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id missing"})
		return
	}
	venueDB := services.DB.ForVenue(venueID)
	order, _ := venueDB.QueryOne(ctx, "orders", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"id": c.Param("orderId")},
	})
	if order == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order})
}

// MobileApproveOrder handles POST /orders/:orderId/approve. Idempotent: a
// request against an already-confirmed order succeeds with the current row.
func MobileApproveOrder(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	if venueID == "" {
		v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
			"select": "id", "where": map[string]interface{}{"is_active": true, "deleted_at": "is.null"}, "limit": 1,
		})
		if v != nil {
			venueID = services.GetString(v, "id")
		}
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	orderID := c.Param("orderId")

	current, _ := venueDB.QueryOne(ctx, "orders", map[string]interface{}{
		"select": "id,status", "where": map[string]interface{}{"id": orderID},
	})
	if current == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	if services.GetString(current, "status") == "confirmed" {
		c.JSON(http.StatusOK, gin.H{"success": true, "order": current, "already_approved": true})
		return
	}

	result, err := venueDB.UpdateCtx(ctx, "orders", map[string]interface{}{
		"status":  "confirmed",
		"paid_at": time.Now().Format(time.RFC3339),
	}, map[string]interface{}{"id": orderID})
	if err != nil || len(result) == 0 {
		log.Printf("[Mobile/ApproveOrder] failed venue=%s id=%s err=%v rows=%d", venueID, orderID, err, len(result))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to approve", "details": fmt.Sprintf("%v", err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "order": result[0]})
}

// MobileRejectOrder handles POST /orders/:orderId/reject
func MobileRejectOrder(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	staffID := c.GetString("staff_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id missing"})
		return
	}
	var body struct{ Reason string `json:"reason"` }
	_ = c.ShouldBindJSON(&body)

	venueDB := services.DB.ForVenue(venueID)
	result, err := venueDB.UpdateCtx(ctx, "orders", map[string]interface{}{
		"status":              "cancelled",
		"cancelled_at":        time.Now().Format(time.RFC3339),
		"cancellation_reason": body.Reason,
		"rejected_by":         staffID,
	}, map[string]interface{}{"id": c.Param("orderId")})
	if err != nil || len(result) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "order": result[0]})
}

// MobileGetGroupReservationDetails handles GET /group-reservations/details/:reservationId
func MobileGetGroupReservationDetails(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	if venueID == "" {
		v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
			"select": "id", "where": map[string]interface{}{"is_active": true, "deleted_at": "is.null"}, "limit": 1,
		})
		if v != nil {
			venueID = services.GetString(v, "id")
		}
	}
	venueDB := services.DB.ForVenue(venueID)
	resv, _ := venueDB.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"id": c.Param("reservationId")},
	})
	if resv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Reservation not found"})
		return
	}
	resvID := services.GetString(resv, "id")
	guests, _ := venueDB.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"reservation_id": resvID},
	})
	bottles, _ := venueDB.QueryCtx(ctx, "vip_list_bottles", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"reservation_id": resvID},
	})
	c.JSON(http.StatusOK, gin.H{
		"reservation": resv,
		"guests":      guests,
		"bottles":     bottles,
	})
}

// MobileApproveGroupReservation handles POST /group-reservations/:id/approve
func MobileApproveGroupReservation(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	staffID := c.GetString("staff_id")
	if venueID == "" {
		v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
			"select": "id", "where": map[string]interface{}{"is_active": true, "deleted_at": "is.null"}, "limit": 1,
		})
		if v != nil {
			venueID = services.GetString(v, "id")
		}
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	id := c.Param("id")
	result, err := venueDB.UpdateCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"status": "confirmed",
	}, map[string]interface{}{"id": id})
	if err != nil || len(result) == 0 {
		log.Printf("[Mobile/ApproveGroupReservation] failed venue=%s id=%s err=%v rows=%d", venueID, id, err, len(result))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to approve", "details": fmt.Sprintf("%v", err)})
		return
	}
	_ = staffID
	c.JSON(http.StatusOK, gin.H{"success": true, "reservation": result[0]})
}

// MobileRejectGroupReservation handles POST /group-reservations/:id/reject
func MobileRejectGroupReservation(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.GetString("venue_id")
	staffID := c.GetString("staff_id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "venue_id missing"})
		return
	}
	venueDB := services.DB.ForVenue(venueID)
	var body struct{ Reason string `json:"reason"` }
	_ = c.ShouldBindJSON(&body)
	result, err := venueDB.UpdateCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"status": "cancelled",
		"notes":  body.Reason,
	}, map[string]interface{}{"id": c.Param("id")})
	if err != nil || len(result) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject"})
		return
	}
	_ = staffID
	c.JSON(http.StatusOK, gin.H{"success": true, "reservation": result[0]})
}

// MobileRegisterPushToken handles POST /notifications/register-token
// Body: { push_token, venue_id, employee_id, device_type }
func MobileRegisterPushToken(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	var req struct {
		PushToken  string `json:"push_token"`
		VenueID    string `json:"venue_id"`
		EmployeeID string `json:"employee_id"`
		DeviceType string `json:"device_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PushToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "push_token is required"})
		return
	}
	venueID := req.VenueID
	if venueID == "" {
		venueID = c.GetString("venue_id")
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}

	// Upsert: check if a row with this push_token already exists.
	existing, _ := venueDB.QueryOne(ctx, "staff_push_tokens", map[string]interface{}{
		"select": "id", "where": map[string]interface{}{"push_token": req.PushToken},
	})
	if existing != nil {
		venueDB.UpdateNoReturn(ctx, "staff_push_tokens", map[string]interface{}{
			"is_active":   true,
			"device_type": req.DeviceType,
		}, map[string]interface{}{"id": services.GetString(existing, "id")})
	} else {
		venueDB.InsertCtx(ctx, "staff_push_tokens", map[string]interface{}{
			"employee_id": req.EmployeeID,
			"push_token":  req.PushToken,
			"device_type": req.DeviceType,
			"is_active":   true,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MobileUnregisterPushToken handles POST /notifications/unregister-token
func MobileUnregisterPushToken(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	var req struct {
		PushToken string `json:"push_token"`
		VenueID   string `json:"venue_id"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.PushToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "push_token is required"})
		return
	}
	venueID := req.VenueID
	if venueID == "" {
		venueID = c.GetString("venue_id")
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	venueDB.UpdateNoReturn(ctx, "staff_push_tokens", map[string]interface{}{
		"is_active": false,
	}, map[string]interface{}{"push_token": req.PushToken})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// fmtSscanInt is a tiny helper that wraps fmt.Sscan so we can swap libs later
// without changing every caller.
func fmtSscanInt(s string, out *int) (int, error) {
	if s == "" {
		return 0, nil
	}
	v := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, nil
		}
		v = v*10 + int(ch-'0')
	}
	*out = v
	return v, nil
}

// resolveVenueID returns the venue id stored on the JWT context, or the first
// active venue if none is set (mobile sometimes calls public endpoints before
// staff context is established).
func resolveVenueID(c *gin.Context) string {
	if vid := c.GetString("venue_id"); vid != "" {
		return vid
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	v, _ := services.DB.Central().QueryOne(ctx, "venues", map[string]interface{}{
		"select": "id", "where": map[string]interface{}{"is_active": true, "deleted_at": "is.null"}, "limit": 1,
	})
	if v != nil {
		return services.GetString(v, "id")
	}
	return ""
}

// MobileGetPendingGuestLists handles GET /guest-lists/venue/:venueId/pending.
// The `guest_list_signups` table doesn't have `venue_id` directly; we look up
// pending signups by event ids belonging to the venue.
func MobileGetPendingGuestLists(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.Param("venueId")
	if venueID == "" {
		venueID = resolveVenueID(c)
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	events, _ := venueDB.QueryCtx(ctx, "events", map[string]interface{}{
		"select": "id", "where": map[string]interface{}{},
	})
	eventIDs := make([]string, 0, len(events))
	for _, e := range events {
		eventIDs = append(eventIDs, services.GetString(e, "id"))
	}
	if len(eventIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"signups": []interface{}{}, "count": 0})
		return
	}
	signups, err := venueDB.QueryCtx(ctx, "guest_list_signups", map[string]interface{}{
		"select": "*",
		"where": map[string]interface{}{
			"event_id": "in.(" + strings.Join(eventIDs, ",") + ")",
			"status":   "pending",
		},
		"order": "created_at.desc",
	})
	if err != nil {
		log.Printf("[Mobile/GuestListsPending] err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load signups", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"signups": signups, "count": len(signups)})
}

// MobileApproveGuestListSignup handles POST /guest-lists/:signupId/approve.
func MobileApproveGuestListSignup(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	result, err := venueDB.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
		"status":      "approved",
		"approved_at": time.Now().Format(time.RFC3339),
	}, map[string]interface{}{"id": c.Param("signupId")})
	if err != nil || len(result) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to approve"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "signup": result[0]})
}

// MobileRejectGuestListSignup handles POST /guest-lists/:signupId/reject.
func MobileRejectGuestListSignup(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	result, err := venueDB.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
		"status":            "rejected",
		"rejection_reason":  body.Reason,
		"rejected_at":       time.Now().Format(time.RFC3339),
	}, map[string]interface{}{"id": c.Param("signupId")})
	if err != nil || len(result) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "signup": result[0]})
}

// MobileBatchApproveGuestList handles POST /guest-lists/batch/approve.
// Body: { signup_ids: ["uuid", ...] }
func MobileBatchApproveGuestList(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	var body struct {
		SignupIDs []string `json:"signup_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.SignupIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "signup_ids required"})
		return
	}
	updated := 0
	for _, id := range body.SignupIDs {
		r, _ := venueDB.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
			"status":      "approved",
			"approved_at": time.Now().Format(time.RFC3339),
		}, map[string]interface{}{"id": id})
		updated += len(r)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "updated": updated})
}

// MobileBatchRejectGuestList handles POST /guest-lists/batch/reject.
func MobileBatchRejectGuestList(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	var body struct {
		SignupIDs []string `json:"signup_ids"`
		Reason    string   `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.SignupIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "signup_ids required"})
		return
	}
	updated := 0
	for _, id := range body.SignupIDs {
		r, _ := venueDB.UpdateCtx(ctx, "guest_list_signups", map[string]interface{}{
			"status":           "rejected",
			"rejection_reason": body.Reason,
			"rejected_at":      time.Now().Format(time.RFC3339),
		}, map[string]interface{}{"id": id})
		updated += len(r)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "updated": updated})
}

// MobileGetGuestListTypes handles GET /guest-lists/types/event/:eventId.
func MobileGetGuestListTypes(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	types, _ := venueDB.QueryCtx(ctx, "guest_list_types", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"event_id": c.Param("eventId")},
		"order": "name.asc",
	})
	c.JSON(http.StatusOK, types)
}

// MobileGetBookings handles GET /bookings/get-bookings/:venueId.
// In this demo, bookings == vip_list_reservations rows that are NOT group
// reservations (group ones have reservation_number starting with "GRP-").
func MobileGetBookings(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueID := c.Param("venueId")
	if venueID == "" {
		venueID = resolveVenueID(c)
	}
	venueDB := services.DB.ForVenue(venueID)
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	page, limit := 1, 20
	_, _ = fmtSscanInt(c.Query("page"), &page)
	_, _ = fmtSscanInt(c.Query("limit"), &limit)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	where := map[string]interface{}{}
	if s := c.Query("status"); s != "" {
		where["status"] = s
	}
	bookings, _ := venueDB.QueryCtx(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*",
		"where":  where,
		"order":  "created_at.desc",
		"limit":  limit,
		"offset": (page - 1) * limit,
	})
	c.JSON(http.StatusOK, gin.H{
		"bookings": bookings,
		"count":    len(bookings),
		"page":     page,
		"limit":    limit,
	})
}

// MobileGetBookingDetails handles GET /bookings/get-booking-details/:bookingId.
func MobileGetBookingDetails(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	resv, _ := venueDB.QueryOne(ctx, "vip_list_reservations", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"id": c.Param("bookingId")},
	})
	if resv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Booking not found"})
		return
	}
	id := services.GetString(resv, "id")
	guests, _ := venueDB.QueryCtx(ctx, "vip_list_guests", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"reservation_id": id},
	})
	bottles, _ := venueDB.QueryCtx(ctx, "vip_list_bottles", map[string]interface{}{
		"select": "*", "where": map[string]interface{}{"reservation_id": id},
	})
	c.JSON(http.StatusOK, gin.H{
		"booking": resv, "guests": guests, "bottles": bottles,
	})
}

// MobileGetEmployees handles GET /employees/employees.
func MobileGetEmployees(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	emps, err := venueDB.QueryCtx(ctx, "organization_workers", map[string]interface{}{
		"select": "id,first_name,last_name,email,role_id,is_active,created_at",
		"where":  map[string]interface{}{"deleted_at": "is.null"},
		"order":  "created_at.desc",
	})
	if err != nil {
		log.Printf("[Mobile/Employees] err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load employees", "details": err.Error()})
		return
	}
	if emps == nil {
		emps = []map[string]interface{}{}
	}
	// Resolve role_id -> human-readable role name for each employee. The
	// mobile UI groups by role string (admin/staff), so a raw uuid would
	// make every employee fall into the "staff" bucket.
	roleMap := loadRoleMap(ctx, venueDB)
	for i, e := range emps {
		if rid := services.GetString(e, "role_id"); rid != "" {
			if name, ok := roleMap[rid]; ok {
				emps[i]["role"] = name
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": emps})
}

// MobileGetEmployee handles GET /employees/employees/:employeeId.
func MobileGetEmployee(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	emp, _ := venueDB.QueryOne(ctx, "organization_workers", map[string]interface{}{
		"select": "id,first_name,last_name,email,role_id,is_active,created_at",
		"where":  map[string]interface{}{"id": c.Param("employeeId"), "deleted_at": "is.null"},
	})
	if emp == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Employee not found"})
		return
	}
	if rid := services.GetString(emp, "role_id"); rid != "" {
		roleMap := loadRoleMap(ctx, venueDB)
		if name, ok := roleMap[rid]; ok {
			emp["role"] = name
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": emp})
}

// loadRoleMap returns a map of role.id -> role.name for the venue. Best-effort:
// if the roles table is missing or empty we return an empty map and the
// mobile UI will fall back to its "staff" default.
func loadRoleMap(ctx context.Context, venueDB *services.SupabaseClient) map[string]string {
	out := map[string]string{}
	if venueDB == nil {
		return out
	}
	roles, _ := venueDB.QueryCtx(ctx, "roles", map[string]interface{}{
		"select": "id,name",
	})
	for _, r := range roles {
		id := services.GetString(r, "id")
		name := services.GetString(r, "name")
		if id != "" && name != "" {
			out[id] = name
		}
	}
	return out
}

// MobileUploadEventImage handles POST /upload/event-image.
// The demo doesn't have S3/Supabase storage configured for staff uploads —
// we accept the payload, return a placeholder image url so the client UI
// can continue, and log what was attempted.
func MobileUploadEventImage(c *gin.Context) {
	var body struct {
		ImageBase64 string `json:"image_base64"`
		ContentType string `json:"content_type"`
		FileName    string `json:"file_name"`
	}
	_ = c.ShouldBindJSON(&body)
	size := len(body.ImageBase64)
	log.Printf("[Mobile/UploadEventImage] file=%s ct=%s size=%d (demo: not persisted)", body.FileName, body.ContentType, size)
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"image_url":  "https://images.unsplash.com/photo-1492684223066-81342ee5ff30?w=1200&q=80",
		"file_name":  body.FileName,
		"size_bytes": size,
		"note":       "demo: upload acepted but not persisted, returned placeholder url",
	})
}

// MobileOrdersSearch handles GET /orders/search/:venueId.
func MobileOrdersSearch(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(c.Param("venueId"))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	search := strings.TrimSpace(c.Query("search"))
	if search == "" {
		c.JSON(http.StatusOK, gin.H{"orders": []interface{}{}, "count": 0})
		return
	}
	page, limit := 1, 20
	_, _ = fmtSscanInt(c.Query("page"), &page)
	_, _ = fmtSscanInt(c.Query("limit"), &limit)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	// Search across customer_name (ilike). PostgREST takes percent-wildcards.
	q := strings.ReplaceAll(search, "%", "")
	orders, _ := venueDB.QueryCtx(ctx, "orders", map[string]interface{}{
		"select": "*",
		"where":  map[string]interface{}{"customer_name": "ilike.*" + q + "*"},
		"order":  "created_at.desc",
		"limit":  limit,
		"offset": (page - 1) * limit,
	})
	c.JSON(http.StatusOK, gin.H{
		"orders": orders,
		"count":  len(orders),
		"page":   page,
		"limit":  limit,
	})
}

// MobileOrdersRefreshView is a no-op endpoint the mobile app pings to ask
// the server to invalidate any cached/materialised views. We have none.
func MobileOrdersRefreshView(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "refreshed": true})
}

// MobileBottleRedemptionPreview / Validate are stubs for the bottle voucher
// scanner. Aurora Hall demo doesn't have any vouchers issued so we always
// answer "not found" — keeps the mobile UI from crashing.
func MobileBottleRedemptionPreview(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"valid":   false,
		"message": "Voucher no encontrado (demo)",
	})
}

func MobileBottleRedemptionValidate(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"valid":   false,
		"message": "Voucher no encontrado (demo)",
	})
}

// MobileGetTicketTypesByEvent handles GET /ticket-types/event/:eventId.
func MobileGetTicketTypesByEvent(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	tts, err := venueDB.QueryCtx(ctx, "ticket_types", map[string]interface{}{
		"select": "*",
		"where":  map[string]interface{}{"event_id": c.Param("eventId")},
		"order":  "price.asc",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not load ticket types"})
		return
	}
	if tts == nil {
		tts = []map[string]interface{}{}
	}
	c.JSON(http.StatusOK, tts)
}

// MobileCreateTicketType handles POST /ticket-types/event/:eventId.
func MobileCreateTicketType(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	var price float64
	switch v := body["price"].(type) {
	case float64:
		price = v
	case int:
		price = float64(v)
	case string:
		fmt.Sscanf(v, "%f", &price)
	}
	payload := map[string]interface{}{
		"event_id":  c.Param("eventId"),
		"name":      services.GetString(body, "name"),
		"price":     price,
		"quantity":  services.GetInt(body, "quantity"),
		"benefits":  services.GetString(body, "benefits"),
		"is_active": true,
	}
	result, err := venueDB.InsertCtx(ctx, "ticket_types", payload)
	if err != nil || result == nil {
		log.Printf("[Mobile/CreateTicketType] err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ticket type", "details": fmt.Sprintf("%v", err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// MobileUpdateTicketType handles PUT /ticket-types/:ticketTypeId.
func MobileUpdateTicketType(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	updates := map[string]interface{}{}
	for _, k := range []string{"name", "benefits", "is_active"} {
		if v, ok := body[k]; ok {
			updates[k] = v
		}
	}
	if v, ok := body["price"]; ok {
		switch p := v.(type) {
		case float64:
			updates["price"] = p
		case int:
			updates["price"] = float64(p)
		case string:
			var f float64
			fmt.Sscanf(p, "%f", &f)
			updates["price"] = f
		}
	}
	if v, ok := body["quantity"]; ok {
		updates["quantity"] = v
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no updates supplied"})
		return
	}
	result, err := venueDB.UpdateCtx(ctx, "ticket_types", updates, map[string]interface{}{
		"id": c.Param("ticketTypeId"),
	})
	if err != nil || len(result) == 0 {
		log.Printf("[Mobile/UpdateTicketType] err=%v rows=%d", err, len(result))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update ticket type"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result[0]})
}

// MobileDeleteTicketType handles DELETE /ticket-types/:ticketTypeId.
// Soft-disable instead of hard delete to preserve order/ticket history.
func MobileDeleteTicketType(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	venueDB := services.DB.ForVenue(resolveVenueID(c))
	if venueDB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid venue"})
		return
	}
	result, err := venueDB.UpdateCtx(ctx, "ticket_types", map[string]interface{}{
		"is_active": false,
	}, map[string]interface{}{"id": c.Param("ticketTypeId")})
	if err != nil || len(result) == 0 {
		log.Printf("[Mobile/DeleteTicketType] err=%v rows=%d", err, len(result))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete ticket type"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
