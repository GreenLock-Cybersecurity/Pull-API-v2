package controllers

import (
	"context"
	"net/http"
	"pull-api-v2/middleware"
	"pull-api-v2/models"
	"pull-api-v2/services"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =============================================
// STAFF NOTIFICATION ENDPOINTS
// =============================================

// GetStaffNotifications returns notifications for authenticated staff member
// GET /staff/notifications
func GetStaffNotifications(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID

	// Query params
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	unreadOnly := c.Query("unread_only") == "true"
	notificationType := c.Query("type")

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Build filter
	filter := map[string]interface{}{
		"venue_id":    venueID,
		"target_type": "staff",
		"is_archived": false,
	}

	// Staff can see notifications targeted to them or their role
	filter["target_id"] = staffID

	if unreadOnly {
		filter["is_read"] = false
	}
	if notificationType != "" {
		filter["type"] = notificationType
	}

	// Get notifications
	results, err := client.QueryCtx(ctx, "notifications", filter)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to fetch notifications", err)
		return
	}

	// Build response
	notifications := make([]models.Notification, 0)
	unreadCount := 0
	for _, row := range results {
		notif := mapToNotification(row)
		notifications = append(notifications, notif)
		if !notif.IsRead {
			unreadCount++
		}
	}

	// Apply pagination (in-memory since Supabase doesn't support offset directly in this helper)
	total := len(notifications)
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginatedNotifications := notifications[start:end]

	response := models.NotificationListResponse{
		Notifications: paginatedNotifications,
		Total:         total,
		UnreadCount:   unreadCount,
		Page:          page,
		Limit:         limit,
	}

	c.JSON(http.StatusOK, response)
}

// MarkNotificationRead marks a notification as read
// PATCH /staff/notifications/:id/read
func MarkNotificationRead(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID
	notificationID := c.Param("id")

	if notificationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Notification ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Verify notification belongs to this staff member
	notifResult, err := client.QueryOne(ctx, "notifications", map[string]interface{}{
		"id":        notificationID,
		"venue_id":  venueID,
		"target_id": staffID,
	})
	if err != nil || notifResult == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}

	// Update as read
	now := time.Now()
	err = client.UpdateNoReturn(ctx, "notifications", map[string]interface{}{
		"is_read": true,
		"read_at": now,
	}, map[string]interface{}{
		"id": notificationID,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to update notification", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Notification marked as read",
		"read_at": now,
	})
}

// MarkAllNotificationsRead marks all notifications as read for staff member
// POST /staff/notifications/read-all
func MarkAllNotificationsRead(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// OPTIMIZED: Single batch update instead of N individual updates
	// First get count for response
	results, err := client.QueryCtx(ctx, "notifications", map[string]interface{}{
		"select":      "id",
		"venue_id":    venueID,
		"target_id":   staffID,
		"target_type": "staff",
		"is_read":     false,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to fetch notifications", err)
		return
	}

	count := len(results)
	now := time.Now()

	// Single batch update using compound WHERE clause
	if count > 0 {
		client.UpdateNoReturn(ctx, "notifications", map[string]interface{}{
			"is_read": true,
			"read_at": now,
		}, map[string]interface{}{
			"venue_id":    venueID,
			"target_id":   staffID,
			"target_type": "staff",
			"is_read":     false,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "All notifications marked as read",
		"count":     count,
		"marked_at": now,
	})
}

// ArchiveNotification archives a notification
// PATCH /staff/notifications/:id/archive
func ArchiveNotification(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID
	notificationID := c.Param("id")

	if notificationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Notification ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Verify notification belongs to this staff member
	notifResult, err := client.QueryOne(ctx, "notifications", map[string]interface{}{
		"id":        notificationID,
		"venue_id":  venueID,
		"target_id": staffID,
	})
	if err != nil || notifResult == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}

	// Archive
	now := time.Now()
	err = client.UpdateNoReturn(ctx, "notifications", map[string]interface{}{
		"is_archived": true,
		"archived_at": now,
	}, map[string]interface{}{
		"id": notificationID,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to archive notification", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Notification archived",
		"archived_at": now,
	})
}

// SearchStaffNotifications searches notifications for staff by text
// GET /staff-notifications/search/:venueId
func SearchStaffNotifications(c *gin.Context) {
	venueID := c.Param("venueId")

	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)

	if staffClaims.VenueID != venueID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// Query params
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	notificationType := c.Query("type")

	if search == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Search query is required"})
		return
	}

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Build filter
	filter := map[string]interface{}{
		"select":      "*",
		"venue_id":    venueID,
		"target_type": "staff",
		"is_archived": false,
		"order":       "created_at.desc",
	}

	if notificationType != "" {
		filter["type"] = notificationType
	}

	// Get all notifications and filter by search in-memory
	// (PostgREST doesn't support ILIKE on title/message easily)
	results, err := client.QueryCtx(ctx, "notifications", filter)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to fetch notifications", err)
		return
	}

	// Filter by search term
	searchLower := strings.ToLower(search)
	filtered := make([]map[string]interface{}, 0)
	for _, row := range results {
		title := strings.ToLower(services.GetString(row, "title"))
		message := strings.ToLower(services.GetString(row, "message"))
		if strings.Contains(title, searchLower) || strings.Contains(message, searchLower) {
			filtered = append(filtered, row)
		}
	}

	// Build response
	total := len(filtered)
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginated := filtered[start:end]

	notifications := make([]models.Notification, 0, len(paginated))
	for _, row := range paginated {
		notifications = append(notifications, mapToNotification(row))
	}

	totalPages := (total + limit - 1) / limit

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    notifications,
		"pagination": gin.H{
			"current_page": page,
			"total_pages":  totalPages,
			"total_count":  total,
			"has_more":     page < totalPages,
			"limit":        limit,
		},
	})
}

// GetVenueStaffNotifications returns notifications for a venue (mobile app path)
// GET /staff-notifications/venue/:venueId
func GetVenueStaffNotifications(c *gin.Context) {
	venueID := c.Param("venueId")

	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)

	if staffClaims.VenueID != venueID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// Query params
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	notificationType := c.Query("type")

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Build filter
	filter := map[string]interface{}{
		"select":      "*",
		"venue_id":    venueID,
		"target_type": "staff",
		"is_archived": false,
		"order":       "created_at.desc",
	}

	if status == "unread" {
		filter["is_read"] = false
	}
	if notificationType != "" {
		filter["type"] = notificationType
	}

	results, err := client.QueryCtx(ctx, "notifications", filter)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to fetch notifications", err)
		return
	}

	// Build response
	total := len(results)
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginated := results[start:end]

	notifications := make([]models.Notification, 0, len(paginated))
	unreadCount := 0
	for _, row := range paginated {
		notif := mapToNotification(row)
		notifications = append(notifications, notif)
		if !notif.IsRead {
			unreadCount++
		}
	}

	totalPages := (total + limit - 1) / limit

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"data":         notifications,
		"unread_count": unreadCount,
		"pagination": gin.H{
			"current_page": page,
			"total_pages":  totalPages,
			"total_count":  total,
			"has_more":     page < totalPages,
			"limit":        limit,
		},
	})
}

// GetUnreadCount returns the count of unread notifications
// GET /staff/notifications/unread-count
func GetUnreadCount(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	results, err := client.QueryCtx(ctx, "notifications", map[string]interface{}{
		"venue_id":    venueID,
		"target_id":   staffID,
		"target_type": "staff",
		"is_read":     false,
		"is_archived": false,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to count notifications", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"unread_count": len(results),
	})
}

// =============================================
// NOTIFICATION PREFERENCES
// =============================================

// GetNotificationPreferences returns staff notification preferences
// GET /staff/notifications/preferences
func GetNotificationPreferences(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	result, err := client.QueryOne(ctx, "notification_preferences", map[string]interface{}{
		"venue_id":    venueID,
		"target_id":   staffID,
		"target_type": "staff",
	})

	if err != nil || result == nil {
		// Return default preferences
		c.JSON(http.StatusOK, models.NotificationPreferences{
			TargetType:   "staff",
			TargetID:     staffID,
			VenueID:      venueID,
			InAppEnabled: true,
			PushEnabled:  true,
			EmailEnabled: true,
			SMSEnabled:   false,
		})
		return
	}

	prefs := mapToNotificationPreferences(result)
	c.JSON(http.StatusOK, prefs)
}

// UpdateNotificationPreferences updates staff notification preferences
// PUT /staff/notifications/preferences
func UpdateNotificationPreferences(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	venueID := staffClaims.VenueID
	staffID := staffClaims.UserID

	var input struct {
		InAppEnabled    *bool   `json:"in_app_enabled"`
		PushEnabled     *bool   `json:"push_enabled"`
		EmailEnabled    *bool   `json:"email_enabled"`
		SMSEnabled      *bool   `json:"sms_enabled"`
		QuietHoursStart *string `json:"quiet_hours_start"`
		QuietHoursEnd   *string `json:"quiet_hours_end"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(venueID)

	// Check if preferences exist
	existing, _ := client.QueryOne(ctx, "notification_preferences", map[string]interface{}{
		"venue_id":    venueID,
		"target_id":   staffID,
		"target_type": "staff",
	})

	now := time.Now()

	if existing == nil {
		// Create new preferences
		newPrefs := map[string]interface{}{
			"id":             uuid.New().String(),
			"venue_id":       venueID,
			"target_id":      staffID,
			"target_type":    "staff",
			"in_app_enabled": true,
			"push_enabled":   true,
			"email_enabled":  true,
			"sms_enabled":    false,
			"created_at":     now,
			"updated_at":     now,
		}

		if input.InAppEnabled != nil {
			newPrefs["in_app_enabled"] = *input.InAppEnabled
		}
		if input.PushEnabled != nil {
			newPrefs["push_enabled"] = *input.PushEnabled
		}
		if input.EmailEnabled != nil {
			newPrefs["email_enabled"] = *input.EmailEnabled
		}
		if input.SMSEnabled != nil {
			newPrefs["sms_enabled"] = *input.SMSEnabled
		}
		if input.QuietHoursStart != nil {
			newPrefs["quiet_hours_start"] = *input.QuietHoursStart
		}
		if input.QuietHoursEnd != nil {
			newPrefs["quiet_hours_end"] = *input.QuietHoursEnd
		}

		_, err := client.InsertCtx(ctx, "notification_preferences", newPrefs)
		if err != nil {
			middleware.SafeError(c, http.StatusInternalServerError, "Failed to create preferences", err)
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"message":     "Preferences created",
			"preferences": newPrefs,
		})
		return
	}

	// Update existing
	updates := map[string]interface{}{
		"updated_at": now,
	}

	if input.InAppEnabled != nil {
		updates["in_app_enabled"] = *input.InAppEnabled
	}
	if input.PushEnabled != nil {
		updates["push_enabled"] = *input.PushEnabled
	}
	if input.EmailEnabled != nil {
		updates["email_enabled"] = *input.EmailEnabled
	}
	if input.SMSEnabled != nil {
		updates["sms_enabled"] = *input.SMSEnabled
	}
	if input.QuietHoursStart != nil {
		updates["quiet_hours_start"] = *input.QuietHoursStart
	}
	if input.QuietHoursEnd != nil {
		updates["quiet_hours_end"] = *input.QuietHoursEnd
	}

	prefsID := services.GetString(existing, "id")
	err := client.UpdateNoReturn(ctx, "notification_preferences", updates, map[string]interface{}{
		"id": prefsID,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to update preferences", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Preferences updated",
	})
}

// =============================================
// PUSH TOKEN MANAGEMENT
// =============================================

// RegisterPushToken registers a device push token for notifications
// POST /staff/notifications/push-token
func RegisterPushToken(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	staffID := staffClaims.UserID

	var input models.RegisterPushTokenRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: device_id, token, and platform are required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Push tokens go to central DB for cross-venue delivery
	client := services.DB.Central()

	// Check if token already exists for this device
	existing, _ := client.QueryOne(ctx, "push_tokens", map[string]interface{}{
		"user_id":   staffID,
		"device_id": input.DeviceID,
	})

	now := time.Now()

	if existing != nil {
		// Update existing token
		tokenID := services.GetString(existing, "id")
		err := client.UpdateNoReturn(ctx, "push_tokens", map[string]interface{}{
			"token":        input.Token,
			"platform":     input.Platform,
			"is_active":    true,
			"updated_at":   now,
			"last_used_at": now,
		}, map[string]interface{}{
			"id": tokenID,
		})
		if err != nil {
			middleware.SafeError(c, http.StatusInternalServerError, "Failed to update push token", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":  "Push token updated",
			"token_id": tokenID,
		})
		return
	}

	// Create new token
	tokenID := uuid.New().String()
	newToken := map[string]interface{}{
		"id":           tokenID,
		"user_id":      staffID,
		"device_id":    input.DeviceID,
		"token":        input.Token,
		"platform":     input.Platform,
		"is_active":    true,
		"created_at":   now,
		"updated_at":   now,
		"last_used_at": now,
	}

	_, err := client.InsertCtx(ctx, "push_tokens", newToken)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to register push token", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":  "Push token registered",
		"token_id": tokenID,
	})
}

// UnregisterPushToken removes a push token
// DELETE /staff/notifications/push-token/:device_id
func UnregisterPushToken(c *gin.Context) {
	claims, exists := c.Get("staff")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	staffClaims := claims.(*models.StaffClaims)
	staffID := staffClaims.UserID
	deviceID := c.Param("device_id")

	if deviceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Device ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.Central()

	// Find and deactivate the token
	existing, err := client.QueryOne(ctx, "push_tokens", map[string]interface{}{
		"user_id":   staffID,
		"device_id": deviceID,
	})
	if err != nil || existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Push token not found"})
		return
	}

	tokenID := services.GetString(existing, "id")
	err = client.UpdateNoReturn(ctx, "push_tokens", map[string]interface{}{
		"is_active":  false,
		"updated_at": time.Now(),
	}, map[string]interface{}{
		"id": tokenID,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to unregister push token", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Push token unregistered",
	})
}

// =============================================
// USER NOTIFICATION ENDPOINTS (for app users)
// =============================================

// GetUserNotifications returns notifications for authenticated user
// GET /users/notifications
func GetUserNotifications(c *gin.Context) {
	claims, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	userClaims := claims.(*models.UserClaims)
	userID := userClaims.UserID

	// Query params
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	unreadOnly := c.Query("unread_only") == "true"

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// User notifications are stored centrally
	client := services.DB.Central()

	filter := map[string]interface{}{
		"target_type": "user",
		"target_id":   userID,
		"is_archived": false,
	}

	if unreadOnly {
		filter["is_read"] = false
	}

	results, err := client.QueryCtx(ctx, "user_notifications", filter)
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to fetch notifications", err)
		return
	}

	notifications := make([]models.Notification, 0)
	unreadCount := 0
	for _, row := range results {
		notif := mapToNotification(row)
		notifications = append(notifications, notif)
		if !notif.IsRead {
			unreadCount++
		}
	}

	// Pagination
	total := len(notifications)
	offset := (page - 1) * limit
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginatedNotifications := notifications[start:end]

	response := models.NotificationListResponse{
		Notifications: paginatedNotifications,
		Total:         total,
		UnreadCount:   unreadCount,
		Page:          page,
		Limit:         limit,
	}

	c.JSON(http.StatusOK, response)
}

// MarkUserNotificationRead marks a user notification as read
// PATCH /users/notifications/:id/read
func MarkUserNotificationRead(c *gin.Context) {
	claims, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	userClaims := claims.(*models.UserClaims)
	userID := userClaims.UserID
	notificationID := c.Param("id")

	if notificationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Notification ID is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.Central()

	// Verify notification belongs to this user
	existing, err := client.QueryOne(ctx, "user_notifications", map[string]interface{}{
		"id":        notificationID,
		"target_id": userID,
	})
	if err != nil || existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}

	now := time.Now()
	err = client.UpdateNoReturn(ctx, "user_notifications", map[string]interface{}{
		"is_read": true,
		"read_at": now,
	}, map[string]interface{}{
		"id": notificationID,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to mark notification as read", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Notification marked as read",
		"read_at": now,
	})
}

// RegisterUserPushToken registers a push token for app user
// POST /users/notifications/push-token
func RegisterUserPushToken(c *gin.Context) {
	claims, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	userClaims := claims.(*models.UserClaims)
	userID := userClaims.UserID

	var input models.RegisterPushTokenRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: device_id, token, and platform are required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	client := services.DB.Central()

	// Check if token exists for this device
	existing, _ := client.QueryOne(ctx, "push_tokens", map[string]interface{}{
		"user_id":   userID,
		"device_id": input.DeviceID,
	})

	now := time.Now()

	if existing != nil {
		tokenID := services.GetString(existing, "id")
		err := client.UpdateNoReturn(ctx, "push_tokens", map[string]interface{}{
			"token":        input.Token,
			"platform":     input.Platform,
			"is_active":    true,
			"updated_at":   now,
			"last_used_at": now,
		}, map[string]interface{}{
			"id": tokenID,
		})
		if err != nil {
			middleware.SafeError(c, http.StatusInternalServerError, "Failed to update push token", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":  "Push token updated",
			"token_id": tokenID,
		})
		return
	}

	// Create new
	tokenID := uuid.New().String()
	_, err := client.InsertCtx(ctx, "push_tokens", map[string]interface{}{
		"id":           tokenID,
		"user_id":      userID,
		"device_id":    input.DeviceID,
		"token":        input.Token,
		"platform":     input.Platform,
		"is_active":    true,
		"created_at":   now,
		"updated_at":   now,
		"last_used_at": now,
	})
	if err != nil {
		middleware.SafeError(c, http.StatusInternalServerError, "Failed to register push token", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":  "Push token registered",
		"token_id": tokenID,
	})
}

// =============================================
// INTERNAL NOTIFICATION CREATION
// =============================================

// CreateNotificationInternal creates a notification in the system
// This is called internally by other controllers
func CreateNotificationInternal(input models.CreateNotificationInput) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := services.DB.ForVenue(input.VenueID)

	now := time.Now()
	notificationID := uuid.New().String()

	notifData := map[string]interface{}{
		"id":              notificationID,
		"venue_id":        input.VenueID,
		"organization_id": input.OrganizationID,
		"type":            string(input.Type),
		"priority":        string(input.Priority),
		"title":           input.Title,
		"message":         input.Message,
		"target_type":     input.TargetType,
		"is_read":         false,
		"is_archived":     false,
		"created_at":      now,
	}

	if input.Data != nil {
		notifData["data"] = input.Data
	}
	if input.TargetID != nil {
		notifData["target_id"] = *input.TargetID
	}
	if input.TargetRole != nil {
		notifData["target_role"] = *input.TargetRole
	}
	if input.EventID != nil {
		notifData["event_id"] = *input.EventID
	}
	if input.OrderID != nil {
		notifData["order_id"] = *input.OrderID
	}
	if input.TicketID != nil {
		notifData["ticket_id"] = *input.TicketID
	}
	if input.ReservationID != nil {
		notifData["group_reservation_id"] = *input.ReservationID
	}
	if input.UserID != nil {
		notifData["user_id"] = *input.UserID
	}
	if input.ActionURL != nil {
		notifData["action_url"] = *input.ActionURL
	}
	if input.ActionText != nil {
		notifData["action_text"] = *input.ActionText
	}
	if len(input.Channels) > 0 {
		channels := make([]string, len(input.Channels))
		for i, ch := range input.Channels {
			channels[i] = string(ch)
		}
		notifData["channels"] = channels
	}

	_, err := client.InsertCtx(ctx, "notifications", notifData)
	return err
}

// CreateUserNotificationInternal creates a notification for an app user (central DB)
func CreateUserNotificationInternal(userID string, notifType models.NotificationType, title, message string, data map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := services.DB.Central()

	now := time.Now()
	notificationID := uuid.New().String()

	notifData := map[string]interface{}{
		"id":          notificationID,
		"target_type": "user",
		"target_id":   userID,
		"type":        string(notifType),
		"priority":    string(models.PriorityNormal),
		"title":       title,
		"message":     message,
		"is_read":     false,
		"is_archived": false,
		"created_at":  now,
	}

	if data != nil {
		notifData["data"] = data
	}

	_, err := client.InsertCtx(ctx, "user_notifications", notifData)
	return err
}

// =============================================
// HELPER FUNCTIONS
// =============================================

func mapToNotification(row map[string]interface{}) models.Notification {
	notif := models.Notification{
		ID:             services.GetString(row, "id"),
		VenueID:        services.GetString(row, "venue_id"),
		OrganizationID: services.GetString(row, "organization_id"),
		Type:           models.NotificationType(services.GetString(row, "type")),
		Priority:       models.NotificationPriority(services.GetString(row, "priority")),
		Title:          services.GetString(row, "title"),
		Message:        services.GetString(row, "message"),
		TargetType:     services.GetString(row, "target_type"),
		IsRead:         services.GetBool(row, "is_read"),
		IsArchived:     services.GetBool(row, "is_archived"),
	}

	if targetID := services.GetString(row, "target_id"); targetID != "" {
		notif.TargetID = &targetID
	}
	if targetRole := services.GetString(row, "target_role"); targetRole != "" {
		notif.TargetRole = &targetRole
	}
	if eventID := services.GetString(row, "event_id"); eventID != "" {
		notif.EventID = &eventID
	}
	if orderID := services.GetString(row, "order_id"); orderID != "" {
		notif.OrderID = &orderID
	}
	if ticketID := services.GetString(row, "ticket_id"); ticketID != "" {
		notif.TicketID = &ticketID
	}
	if grID := services.GetString(row, "group_reservation_id"); grID != "" {
		notif.GroupReservationID = &grID
	}
	if vipID := services.GetString(row, "vip_list_id"); vipID != "" {
		notif.VIPListID = &vipID
	}
	if glsID := services.GetString(row, "guest_list_signup_id"); glsID != "" {
		notif.GuestListSignupID = &glsID
	}
	if userID := services.GetString(row, "user_id"); userID != "" {
		notif.UserID = &userID
	}
	if actionURL := services.GetString(row, "action_url"); actionURL != "" {
		notif.ActionURL = &actionURL
	}
	if actionText := services.GetString(row, "action_text"); actionText != "" {
		notif.ActionText = &actionText
	}

	// Parse timestamps
	if createdAt, ok := row["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			notif.CreatedAt = t
		}
	}
	if readAt, ok := row["read_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, readAt); err == nil {
			notif.ReadAt = &t
		}
	}
	if archivedAt, ok := row["archived_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, archivedAt); err == nil {
			notif.ArchivedAt = &t
		}
	}
	if deliveredAt, ok := row["delivered_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, deliveredAt); err == nil {
			notif.DeliveredAt = &t
		}
	}
	if expiresAt, ok := row["expires_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			notif.ExpiresAt = &t
		}
	}

	// Parse data/metadata
	if data, ok := row["data"].(map[string]interface{}); ok {
		notif.Data = data
	}

	return notif
}

func mapToNotificationPreferences(row map[string]interface{}) models.NotificationPreferences {
	prefs := models.NotificationPreferences{
		ID:           services.GetString(row, "id"),
		TargetType:   services.GetString(row, "target_type"),
		TargetID:     services.GetString(row, "target_id"),
		VenueID:      services.GetString(row, "venue_id"),
		InAppEnabled: services.GetBool(row, "in_app_enabled"),
		PushEnabled:  services.GetBool(row, "push_enabled"),
		EmailEnabled: services.GetBool(row, "email_enabled"),
		SMSEnabled:   services.GetBool(row, "sms_enabled"),
	}

	if qhs := services.GetString(row, "quiet_hours_start"); qhs != "" {
		prefs.QuietHoursStart = &qhs
	}
	if qhe := services.GetString(row, "quiet_hours_end"); qhe != "" {
		prefs.QuietHoursEnd = &qhe
	}
	if fcm := services.GetString(row, "fcm_token"); fcm != "" {
		prefs.FCMToken = &fcm
	}
	if apns := services.GetString(row, "apns_token"); apns != "" {
		prefs.APNSToken = &apns
	}

	// Parse timestamps
	if createdAt, ok := row["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			prefs.CreatedAt = t
		}
	}
	if updatedAt, ok := row["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			prefs.UpdatedAt = t
		}
	}

	return prefs
}
