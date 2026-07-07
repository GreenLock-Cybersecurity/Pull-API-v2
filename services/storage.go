package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// =============================================
// SUPABASE STORAGE SERVICE
// Handles secure file uploads/downloads with signed URLs
// =============================================

// StorageService handles file operations with Supabase Storage
type StorageService struct {
	baseURL    string
	serviceKey string
	client     *http.Client
}

// NewStorageService creates a new storage service for a venue
func NewStorageService(supabaseURL, serviceKey string) *StorageService {
	return &StorageService{
		baseURL:    supabaseURL,
		serviceKey: serviceKey,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// AllowedImageTypes defines permitted image MIME types
var AllowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// MaxFileSize is the maximum allowed file size (5MB)
const MaxFileSize = 5 * 1024 * 1024

// ExtensionToMimeType maps file extensions to MIME types
var ExtensionToMimeType = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// =============================================
// MAGIC BYTES VALIDATION (Security)
// Validates actual file content, not just headers
// =============================================

// imageMagicBytes defines the magic byte signatures for each image type
// This prevents attackers from uploading malicious files with fake extensions
var imageMagicBytes = map[string][]byte{
	"image/jpeg": {0xFF, 0xD8, 0xFF},                 // JPEG starts with FF D8 FF
	"image/png":  {0x89, 0x50, 0x4E, 0x47},           // PNG: 89 50 4E 47 (.PNG)
	"image/gif":  {0x47, 0x49, 0x46, 0x38},           // GIF: 47 49 46 38 (GIF8)
	"image/webp": {0x52, 0x49, 0x46, 0x46},           // WebP: 52 49 46 46 (RIFF)
}

// webpSignature is the additional check for WebP files (WEBP after RIFF header)
var webpSignature = []byte{0x57, 0x45, 0x42, 0x50} // WEBP

// ValidateImageMagicBytes validates the file content against known magic bytes
// SECURITY: This prevents attacks where malicious files are disguised as images
// Returns the detected MIME type and an error if invalid
func ValidateImageMagicBytes(data []byte) (string, error) {
	if len(data) < 12 {
		return "", fmt.Errorf("file too small to be a valid image")
	}

	// Check JPEG (FF D8 FF)
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", nil
	}

	// Check PNG (89 50 4E 47 0D 0A 1A 0A)
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png", nil
	}

	// Check GIF (47 49 46 38 37/39 61)
	if len(data) >= 6 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x38 {
		if data[4] == 0x37 || data[4] == 0x39 { // GIF87a or GIF89a
			return "image/gif", nil
		}
	}

	// Check WebP (RIFF....WEBP)
	if len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 {
		// Check for WEBP signature at offset 8
		if data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
			return "image/webp", nil
		}
	}

	return "", fmt.Errorf("file content does not match any allowed image format")
}

// UploadFile uploads a file to Supabase Storage
// SECURITY: Validates both file extension AND magic bytes to prevent malicious uploads
func (s *StorageService) UploadFile(ctx context.Context, bucket, path string, file multipart.File, header *multipart.FileHeader) (string, error) {
	// Validate file size
	if header.Size > MaxFileSize {
		return "", fmt.Errorf("file too large: max size is 5MB")
	}

	// Get content type from header, or detect from extension
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		// Try to detect from file extension
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if mimeType, ok := ExtensionToMimeType[ext]; ok {
			contentType = mimeType
		}
	}

	// First check: extension/header-based type check
	if !AllowedImageTypes[contentType] {
		return "", fmt.Errorf("invalid file type: only JPEG, PNG, GIF, and WebP are allowed (got: %s)", contentType)
	}

	// Read file content
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// SECURITY: Validate magic bytes to ensure file content matches claimed type
	// This prevents attacks where malicious files are disguised with fake extensions
	detectedType, err := ValidateImageMagicBytes(fileBytes)
	if err != nil {
		return "", fmt.Errorf("security validation failed: %w", err)
	}

	// Verify that detected type matches the claimed type
	// Allow JPEG variants (image/jpeg matches detected jpeg)
	if detectedType != contentType {
		return "", fmt.Errorf("file content does not match declared type (declared: %s, detected: %s)", contentType, detectedType)
	}

	// Build upload URL
	uploadURL := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, bucket, path)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, bytes.NewReader(fileBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.serviceKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true") // Overwrite if exists

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Return the path (not a public URL)
	return path, nil
}

// DeleteFile deletes a file from Supabase Storage
func (s *StorageService) DeleteFile(ctx context.Context, bucket, path string) error {
	deleteURL := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, bucket, path)

	req, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.serviceKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GenerateSignedURL creates a signed URL for secure access to a file
func (s *StorageService) GenerateSignedURL(ctx context.Context, bucket, path string, expiresIn time.Duration) (string, error) {
	// Supabase uses POST to /storage/v1/object/sign/{bucket}/{path}
	signURL := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", s.baseURL, bucket, path)

	// Create request body with expiration
	expiresInSeconds := int(expiresIn.Seconds())
	body := fmt.Sprintf(`{"expiresIn":%d}`, expiresInSeconds)

	req, err := http.NewRequestWithContext(ctx, "POST", signURL, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.serviceKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sign request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sign failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Simple JSON parsing
	signedURLStart := strings.Index(string(respBody), `"signedURL":"`)
	if signedURLStart == -1 {
		return "", fmt.Errorf("signedURL not found in response: %s", string(respBody))
	}

	signedURLStart += len(`"signedURL":"`)
	signedURLEnd := strings.Index(string(respBody)[signedURLStart:], `"`)
	if signedURLEnd == -1 {
		return "", fmt.Errorf("invalid signedURL format")
	}

	signedPath := string(respBody)[signedURLStart : signedURLStart+signedURLEnd]

	// Build full URL
	fullURL := fmt.Sprintf("%s/storage/v1%s", s.baseURL, signedPath)

	return fullURL, nil
}

// GenerateSignedUploadURL creates a signed URL for uploading a file
func (s *StorageService) GenerateSignedUploadURL(ctx context.Context, bucket, path string, expiresIn time.Duration) (string, error) {
	// Supabase uses POST to /storage/v1/object/upload/sign/{bucket}/{path}
	signURL := fmt.Sprintf("%s/storage/v1/object/upload/sign/%s/%s", s.baseURL, bucket, path)

	req, err := http.NewRequestWithContext(ctx, "POST", signURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.serviceKey)
	req.Header.Set("x-upsert", "true")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sign upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sign upload failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the token from response
	tokenStart := strings.Index(string(respBody), `"token":"`)
	if tokenStart == -1 {
		// Try alternate format with url field
		urlStart := strings.Index(string(respBody), `"url":"`)
		if urlStart != -1 {
			urlStart += len(`"url":"`)
			urlEnd := strings.Index(string(respBody)[urlStart:], `"`)
			if urlEnd != -1 {
				signedPath := string(respBody)[urlStart : urlStart+urlEnd]
				return fmt.Sprintf("%s/storage/v1%s", s.baseURL, signedPath), nil
			}
		}
		return "", fmt.Errorf("token/url not found in response: %s", string(respBody))
	}

	tokenStart += len(`"token":"`)
	tokenEnd := strings.Index(string(respBody)[tokenStart:], `"`)
	if tokenEnd == -1 {
		return "", fmt.Errorf("invalid token format")
	}

	token := string(respBody)[tokenStart : tokenStart+tokenEnd]

	// Build upload URL with token
	uploadURL := fmt.Sprintf("%s/storage/v1/object/upload/sign/%s/%s?token=%s", s.baseURL, bucket, path, url.QueryEscape(token))

	return uploadURL, nil
}

// GetStorageForVenue returns a storage service for a specific venue
func GetStorageForVenue(ctx context.Context, venueID string) (*StorageService, error) {
	central := DB.Central()
	if central == nil {
		return nil, fmt.Errorf("central database not available")
	}

	// Get venue database config
	config, err := central.QueryOne(ctx, "venue_database_configs", map[string]interface{}{
		"select": "supabase_url,service_key_encrypted",
		"where": map[string]interface{}{
			"venue_id":  venueID,
			"is_active": true,
		},
	})

	if err != nil || config == nil {
		return nil, fmt.Errorf("venue database config not found")
	}

	supabaseURL := GetString(config, "supabase_url")
	encryptedKey := GetString(config, "service_key_encrypted")

	if supabaseURL == "" || encryptedKey == "" {
		return nil, fmt.Errorf("incomplete venue database config")
	}

	// Decrypt service key
	serviceKey, err := DecryptServiceKey(encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt service key: %w", err)
	}

	return NewStorageService(supabaseURL, serviceKey), nil
}

// GetCentralStorage returns a storage service for the central database
func GetCentralStorage() (*StorageService, error) {
	central := DB.Central()
	if central == nil {
		return nil, fmt.Errorf("central database not available")
	}

	// Get central Supabase credentials from environment
	supabaseURL := central.GetBaseURL()
	serviceKey := central.GetServiceKey()

	if supabaseURL == "" || serviceKey == "" {
		return nil, fmt.Errorf("central storage not configured")
	}

	return NewStorageService(supabaseURL, serviceKey), nil
}

// GenerateUniqueFileName generates a unique file name preserving the extension
func GenerateUniqueFileName(originalName string) string {
	ext := filepath.Ext(originalName)
	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", originalName, timestamp)))
	return fmt.Sprintf("%s%s", hex.EncodeToString(hash[:8]), ext)
}

// ValidateImageFile validates that a file is a valid image
func ValidateImageFile(header *multipart.FileHeader) error {
	if header.Size > MaxFileSize {
		return fmt.Errorf("file too large: maximum size is 5MB")
	}

	// Get content type from header, or detect from extension
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if mimeType, ok := ExtensionToMimeType[ext]; ok {
			contentType = mimeType
		}
	}

	if !AllowedImageTypes[contentType] {
		return fmt.Errorf("invalid file type: only JPEG, PNG, GIF, and WebP are allowed (got: %s)", contentType)
	}

	return nil
}

// GenerateHMAC generates an HMAC signature for URL validation
func GenerateHMAC(message, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// ValidateHMAC validates an HMAC signature
func ValidateHMAC(message, signature, secret string) bool {
	expected := GenerateHMAC(message, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}
