package controllers

import (
	"pull-api-v2/models"
)

// models_CheckoutParams is a small constructor for CheckoutParams used by the
// legacy compat controller, keeping the call site readable.
func models_CheckoutParams(amount float64, currency, orderID, productName, customerEmail, successURL, cancelURL, venueID, eventID, orderNumber string) models.CheckoutParams {
	return models.CheckoutParams{
		Amount:        amount,
		Currency:      currency,
		OrderID:       orderID,
		ProductName:   productName,
		CustomerEmail: customerEmail,
		SuccessURL:    successURL,
		CancelURL:     cancelURL,
		VenueID:       venueID,
		EventID:       eventID,
		Metadata: map[string]string{
			"venue_id":     venueID,
			"order_id":     orderID,
			"event_id":     eventID,
			"order_number": orderNumber,
		},
	}
}
