package controllers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"pull-api-v2/services"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================
// STORAGE CONTROLLER
// Handles secure image uploads for venues
// =============================================

const (
	// VenueImagesBucket is the bucket name for venue images in central Supabase
	VenueImagesBucket = "venue-images"
	// SignedURLExpiration is the default expiration for signed URLs
	SignedURLExpiration = 1 * time.Hour
	// UploadURLExpiration is the expiration for upload URLs
	UploadURLExpiration = 15 * time.Minute
)

// UploadVenueImage handles direct image upload for a venue
// POST /api/v1/secure-admin/venues/:id/images
func UploadVenueImage(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	venueID := c.Param("id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Venue ID is required"})
		return
	}

	// Get image type from query (image, cover_image, gallery)
	imageType := c.DefaultQuery("type", "image")
	if imageType != "image" && imageType != "cover_image" && imageType != "gallery" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image type. Must be: image, cover_image, or gallery"})
		return
	}

	// Parse multipart form
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file provided", "details": err.Error()})
		return
	}
	defer file.Close()

	// Validate file
	if err := services.ValidateImageFile(header); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get central storage service
	storage, err := services.GetCentralStorage()
	if err != nil {
		log.Printf("[UploadVenueImage] Failed to get storage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage not available"})
		return
	}

	// Generate unique filename
	uniqueName := services.GenerateUniqueFileName(header.Filename)

	// Build path: venues/{venue_id}/{type}/{filename}
	storagePath := fmt.Sprintf("venues/%s/%s/%s", venueID, imageType, uniqueName)

	// Upload file
	path, err := storage.UploadFile(ctx, VenueImagesBucket, storagePath, file, header)
	if err != nil {
		log.Printf("[UploadVenueImage] Upload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload image", "details": err.Error()})
		return
	}

	// Generate signed URL for immediate use
	signedURL, err := storage.GenerateSignedURL(ctx, VenueImagesBucket, path, SignedURLExpiration)
	if err != nil {
		log.Printf("[UploadVenueImage] Failed to generate signed URL: %v", err)
		// Still return success, just without the signed URL
		c.JSON(http.StatusOK, gin.H{
			"message": "Image uploaded successfully",
			"path":    path,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Image uploaded successfully",
		"path":       path,
		"signed_url": signedURL,
		"expires_in": SignedURLExpiration.Seconds(),
	})
}

// GetVenueImageURL generates a signed URL for accessing a venue image
// GET /api/v1/secure-admin/venues/:id/images/sign
func GetVenueImageURL(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.Param("id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Venue ID is required"})
		return
	}

	// Get path from query
	imagePath := c.Query("path")
	if imagePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Image path is required"})
		return
	}

	// Security: Ensure the path belongs to this venue
	expectedPrefix := fmt.Sprintf("venues/%s/", venueID)
	if !strings.HasPrefix(imagePath, expectedPrefix) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied to this image"})
		return
	}

	// Get central storage service
	storage, err := services.GetCentralStorage()
	if err != nil {
		log.Printf("[GetVenueImageURL] Failed to get storage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage not available"})
		return
	}

	// Generate signed URL
	signedURL, err := storage.GenerateSignedURL(ctx, VenueImagesBucket, imagePath, SignedURLExpiration)
	if err != nil {
		log.Printf("[GetVenueImageURL] Failed to generate signed URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate URL", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"signed_url": signedURL,
		"expires_in": SignedURLExpiration.Seconds(),
		"path":       imagePath,
	})
}

// DeleteVenueImage deletes a venue image
// DELETE /api/v1/secure-admin/venues/:id/images
func DeleteVenueImage(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.Param("id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Venue ID is required"})
		return
	}

	// Get path from query
	imagePath := c.Query("path")
	if imagePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Image path is required"})
		return
	}

	// Security: Ensure the path belongs to this venue
	expectedPrefix := fmt.Sprintf("venues/%s/", venueID)
	if !strings.HasPrefix(imagePath, expectedPrefix) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied to this image"})
		return
	}

	// Get central storage service
	storage, err := services.GetCentralStorage()
	if err != nil {
		log.Printf("[DeleteVenueImage] Failed to get storage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage not available"})
		return
	}

	// Delete file
	if err := storage.DeleteFile(ctx, VenueImagesBucket, imagePath); err != nil {
		log.Printf("[DeleteVenueImage] Delete failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete image", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Image deleted successfully",
		"path":    imagePath,
	})
}

// GetSignedUploadURL generates a signed URL for client-side upload
// POST /api/v1/secure-admin/venues/:id/images/upload-url
func GetSignedUploadURL(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	venueID := c.Param("id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Venue ID is required"})
		return
	}

	var req struct {
		Filename  string `json:"filename" binding:"required"`
		ImageType string `json:"image_type"` // image, cover_image, gallery
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	// Default to "image" type
	if req.ImageType == "" {
		req.ImageType = "image"
	}

	// Validate image type
	if req.ImageType != "image" && req.ImageType != "cover_image" && req.ImageType != "gallery" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image type"})
		return
	}

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(req.Filename))
	validExtensions := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
	if !validExtensions[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Allowed: jpg, jpeg, png, gif, webp"})
		return
	}

	// Get central storage service
	storage, err := services.GetCentralStorage()
	if err != nil {
		log.Printf("[GetSignedUploadURL] Failed to get storage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage not available"})
		return
	}

	// Generate unique filename
	uniqueName := services.GenerateUniqueFileName(req.Filename)
	storagePath := fmt.Sprintf("venues/%s/%s/%s", venueID, req.ImageType, uniqueName)

	// Generate signed upload URL
	uploadURL, err := storage.GenerateSignedUploadURL(ctx, VenueImagesBucket, storagePath, UploadURLExpiration)
	if err != nil {
		log.Printf("[GetSignedUploadURL] Failed to generate upload URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate upload URL", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_url": uploadURL,
		"path":       storagePath,
		"expires_in": UploadURLExpiration.Seconds(),
	})
}

// BatchGetSignedURLs generates signed URLs for multiple images
// POST /api/v1/secure-admin/venues/:id/images/batch-sign
func BatchGetSignedURLs(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	venueID := c.Param("id")
	if venueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Venue ID is required"})
		return
	}

	var req struct {
		Paths []string `json:"paths" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	if len(req.Paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No paths provided"})
		return
	}

	if len(req.Paths) > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Maximum 20 paths allowed per request"})
		return
	}

	// Get central storage service
	storage, err := services.GetCentralStorage()
	if err != nil {
		log.Printf("[BatchGetSignedURLs] Failed to get storage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage not available"})
		return
	}

	// Security: Ensure all paths belong to this venue
	expectedPrefix := fmt.Sprintf("venues/%s/", venueID)
	results := make([]map[string]interface{}, 0, len(req.Paths))

	for _, path := range req.Paths {
		result := map[string]interface{}{
			"path": path,
		}

		if !strings.HasPrefix(path, expectedPrefix) {
			result["error"] = "Access denied"
			results = append(results, result)
			continue
		}

		signedURL, err := storage.GenerateSignedURL(ctx, VenueImagesBucket, path, SignedURLExpiration)
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["signed_url"] = signedURL
			result["expires_in"] = SignedURLExpiration.Seconds()
		}

		results = append(results, result)
	}

	c.JSON(http.StatusOK, gin.H{
		"results": results,
	})
}
