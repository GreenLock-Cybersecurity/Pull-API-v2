package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"pull-api-v2/config"
	"pull-api-v2/models"
)

// MockProcessor simulates a payment gateway end-to-end for demo deployments.
//
// CreateCheckout returns a URL to an HTML page served by this same API that
// renders a fake "Pagar ahora" UX. When the user confirms, the page calls
// /api/v1/orders/confirm which routes back to ConfirmPayment, which in turn
// calls MockProcessor.ConfirmPayment — always returning success.
//
// Enabled by setting DEMO_MODE=true in the environment.
type MockProcessor struct{}

func NewMockProcessor() *MockProcessor {
	return &MockProcessor{}
}

func (p *MockProcessor) GetGateway() models.PaymentGateway {
	// Identify as Stripe so existing frontend copy/branding works seamlessly.
	return models.GatewayStripe
}

func (p *MockProcessor) CreateCheckout(ctx context.Context, params models.CheckoutParams) (*models.CheckoutResult, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	sessionID := "mock_" + hex.EncodeToString(b)

	apiBaseURL := config.App.APIBaseURL
	if apiBaseURL == "" {
		apiBaseURL = fmt.Sprintf("http://localhost:%s", config.App.Port)
	}

	venueID := params.VenueID
	if venueID == "" {
		venueID = params.Metadata["venue_id"]
	}

	successURL := params.SuccessURL
	if successURL == "" {
		successURL = config.App.FrontendURL
	}
	cancelURL := params.CancelURL
	if cancelURL == "" {
		cancelURL = successURL
	}

	checkoutURL := fmt.Sprintf(
		"%s/api/v1/orders/demo-checkout?session_id=%s&venue_id=%s&success=%s&cancel=%s&amount=%.2f&currency=%s&product=%s",
		apiBaseURL,
		sessionID,
		venueID,
		url.QueryEscape(successURL),
		url.QueryEscape(cancelURL),
		params.Amount,
		url.QueryEscape(params.Currency),
		url.QueryEscape(params.ProductName),
	)

	return &models.CheckoutResult{
		SessionID:   sessionID,
		CheckoutURL: checkoutURL,
		Gateway:     models.GatewayStripe,
	}, nil
}

func (p *MockProcessor) ConfirmPayment(ctx context.Context, sessionID string) (*models.PaymentResult, error) {
	tail := sessionID
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	return &models.PaymentResult{
		Success:           true,
		TransactionID:     sessionID,
		AuthorizationCode: "DEMO-" + tail,
		Gateway:           models.GatewayStripe,
		CardLast4:         "0000",
		CardBrand:         "demo",
	}, nil
}

func (p *MockProcessor) ProcessRefund(ctx context.Context, transactionID string, amount float64) error {
	return nil
}

func (p *MockProcessor) ValidateWebhook(payload []byte, signature string) (bool, error) {
	return true, nil
}
