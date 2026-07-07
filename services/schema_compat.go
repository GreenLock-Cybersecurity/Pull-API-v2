package services

import (
	"time"
)

// venueLocation is the IANA timezone used when materializing event_date /
// start_time / end_time for the frontend. The demo venue (Aurora Hall) is in
// Guatemala City (UTC-06:00). For multi-tenant production this should come
// from the venue row, but for the single-venue demo we hardcode it here.
var venueLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Guatemala")
	if err != nil {
		return time.FixedZone("GT", -6*3600)
	}
	return loc
}()

// Schema compatibility helpers.
//
// The real venue DB schema uses canonical column names (start_datetime,
// quantity_total, price_male, etc.) but several controllers and the existing
// frontend were written against an older schema with names like event_date,
// available_quantity, male_price. Rather than refactor every reference, these
// helpers enrich response rows with the legacy field aliases AFTER reading.
//
// Usage:
//	events, _ := venueDB.QueryCtx(ctx, "events", params)
//	EnrichEvents(events)
//	c.JSON(200, gin.H{"events": events})

// EnrichEvent adds legacy field aliases to a single event row.
// Times are converted from UTC (Supabase storage) to the venue's local
// timezone before being formatted.
func EnrichEvent(ev map[string]interface{}) {
	if ev == nil {
		return
	}
	if dt := GetString(ev, "start_datetime"); dt != "" {
		if t, err := time.Parse(time.RFC3339, dt); err == nil {
			local := t.In(venueLocation)
			ev["event_date"] = local.Format("2006-01-02")
			ev["start_time"] = local.Format("15:04:05")
		}
	}
	if dt := GetString(ev, "end_datetime"); dt != "" {
		if t, err := time.Parse(time.RFC3339, dt); err == nil {
			ev["end_time"] = t.In(venueLocation).Format("15:04:05")
		}
	}
	if loc := GetString(ev, "location"); loc != "" {
		if _, ok := ev["custom_location"]; !ok {
			ev["custom_location"] = loc
		}
	}
	if cap, ok := ev["capacity"]; ok {
		if _, exists := ev["ticket_limit"]; !exists {
			ev["ticket_limit"] = cap
		}
	}
	// status -> is_active / is_published flags expected by frontend
	status := GetString(ev, "status")
	if _, exists := ev["is_active"]; !exists {
		ev["is_active"] = (status != "cancelled" && status != "archived")
	}
	if _, exists := ev["is_published"]; !exists {
		ev["is_published"] = (status == "published" || status == "active" || status == "live")
	}
	if _, exists := ev["table_capacity"]; !exists {
		ev["table_capacity"] = 0
	}
	if _, exists := ev["requirements"]; !exists {
		ev["requirements"] = []interface{}{}
	}

	// Legacy frontend field names (PullWebApp-GL TypeScript types).
	if v, ok := ev["id"]; ok {
		if _, exists := ev["event_id"]; !exists {
			ev["event_id"] = v
		}
	}
	if v, ok := ev["slug"]; ok {
		if _, exists := ev["event_slug"]; !exists {
			ev["event_slug"] = v
		}
	}
	if v, ok := ev["name"]; ok {
		if _, exists := ev["event_name"]; !exists {
			ev["event_name"] = v
		}
	}
	if v, ok := ev["image"]; ok {
		if _, exists := ev["event_img"]; !exists {
			ev["event_img"] = v
		}
	}
	if _, exists := ev["currency"]; !exists {
		ev["currency"] = "GTQ"
	}
}

// EnrichEvents enriches a list of event rows in place.
func EnrichEvents(events []map[string]interface{}) {
	for i := range events {
		EnrichEvent(events[i])
	}
}

// EnrichTicketType adds legacy field aliases to a ticket_types row.
//
//	quantity_total - quantity_sold - quantity_reserved -> available_quantity
//	quantity_total                                     -> initial_quantity
//	min_per_order / max_per_order                       -> min_quantity / max_quantity
//	price                                              -> base_price
//	has_gender_pricing / male_price / female_price     -> false / 0 / 0 (regular tickets don't have gender pricing)
//	is_group                                           -> false
func EnrichTicketType(tt map[string]interface{}) {
	if tt == nil {
		return
	}
	total := GetInt(tt, "quantity_total")
	sold := GetInt(tt, "quantity_sold")
	reserved := GetInt(tt, "quantity_reserved")
	available := total - sold - reserved
	if available < 0 {
		available = 0
	}
	if _, ok := tt["available_quantity"]; !ok {
		tt["available_quantity"] = available
	}
	if _, ok := tt["initial_quantity"]; !ok {
		tt["initial_quantity"] = total
	}
	if _, ok := tt["min_quantity"]; !ok {
		tt["min_quantity"] = GetInt(tt, "min_per_order")
	}
	if _, ok := tt["max_quantity"]; !ok {
		tt["max_quantity"] = GetInt(tt, "max_per_order")
	}
	if _, ok := tt["base_price"]; !ok {
		tt["base_price"] = GetFloat64(tt, "price")
	}
	if _, ok := tt["has_gender_pricing"]; !ok {
		tt["has_gender_pricing"] = false
	}
	if _, ok := tt["male_price"]; !ok {
		tt["male_price"] = 0.0
	}
	if _, ok := tt["female_price"]; !ok {
		tt["female_price"] = 0.0
	}
	if _, ok := tt["is_group"]; !ok {
		tt["is_group"] = false
	}

	// Legacy frontend field names (PullWebApp-GL TicketType type).
	if v, ok := tt["id"]; ok {
		if _, exists := tt["ticket_type_id"]; !exists {
			tt["ticket_type_id"] = v
		}
	}
	if v, ok := tt["name"]; ok {
		if _, exists := tt["ticket_name"]; !exists {
			tt["ticket_name"] = v
		}
	}
	if v, ok := tt["price"]; ok {
		if _, exists := tt["ticket_price"]; !exists {
			tt["ticket_price"] = v
		}
	}
	if v, ok := tt["description"]; ok {
		if _, exists := tt["ticket_description"]; !exists {
			tt["ticket_description"] = v
		}
	}
	if v, ok := tt["available_quantity"]; ok {
		if _, exists := tt["ticket_quantity"]; !exists {
			tt["ticket_quantity"] = v
		}
	}
}

// EnrichTicketTypes enriches a list of ticket_types rows.
func EnrichTicketTypes(rows []map[string]interface{}) {
	for i := range rows {
		EnrichTicketType(rows[i])
	}
}

// EnrichVIPTicketType maps event_vip_ticket_types canonical names to legacy aliases:
//	price_male  -> male_price
//	price_female -> female_price
//	max_guests   -> max_quantity (when not set)
func EnrichVIPTicketType(vt map[string]interface{}) {
	if vt == nil {
		return
	}
	if v, ok := vt["price_male"]; ok {
		if _, exists := vt["male_price"]; !exists {
			vt["male_price"] = v
		}
	}
	if v, ok := vt["price_female"]; ok {
		if _, exists := vt["female_price"]; !exists {
			vt["female_price"] = v
		}
	}
	if v, ok := vt["max_guests"]; ok {
		if _, exists := vt["max_quantity"]; !exists {
			vt["max_quantity"] = v
		}
	}
	if _, ok := vt["min_quantity"]; !ok {
		vt["min_quantity"] = 1
	}
}

// EnrichVIPTicketTypes enriches a list of event_vip_ticket_types rows.
func EnrichVIPTicketTypes(rows []map[string]interface{}) {
	for i := range rows {
		EnrichVIPTicketType(rows[i])
	}
}

// EventSelectColumns returns the canonical events column list to SELECT.
const EventSelectColumns = "id,name,slug,description,short_description,image,cover_image,gallery,start_datetime,end_datetime,doors_open_time,location,address,capacity,status,is_featured,is_private,min_age,dress_code,music_genres,artists,use_vip_flow,use_guest_list,require_approval,tickets_sold"

// EventListSelectColumns is a smaller column set for list views.
const EventListSelectColumns = "id,name,slug,image,start_datetime,end_datetime,location,capacity,min_age,status,is_featured"

// TicketTypeSelectColumns returns the canonical ticket_types column list.
const TicketTypeSelectColumns = "id,event_id,name,description,price,currency,quantity_total,quantity_sold,quantity_reserved,max_per_order,min_per_order,is_active,is_visible,sort_order,benefits"

// VIPTicketTypeSelectColumns returns the canonical event_vip_ticket_types column list.
const VIPTicketTypeSelectColumns = "id,event_id,name,description,price_male,price_female,currency,quantity_total,quantity_sold,includes_bottles,includes_table,max_guests,sort_order"

// PublishedEventStatuses is the where-clause value used to filter "live"
// events. The event_status enum in the venue DB only includes "published" as
// the user-visible state (alongside draft/cancelled/archived), so we just
// match that single value.
const PublishedEventStatuses = "eq.published"
