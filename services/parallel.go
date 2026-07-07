package services

import (
	"context"
	"sync"
)

// =============================================
// PARALLEL QUERY EXECUTION
// Execute multiple database queries concurrently
// =============================================

// QueryResult holds the result of a parallel query
type QueryResult struct {
	Data  []map[string]interface{}
	Error error
}

// SingleResult holds the result of a single-row query
type SingleResult struct {
	Data  map[string]interface{}
	Error error
}

// ParallelQueries executes multiple queries in parallel and returns results
func ParallelQueries(ctx context.Context, db *SupabaseClient, queries map[string]map[string]interface{}) map[string]*QueryResult {
	results := make(map[string]*QueryResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, params := range queries {
		wg.Add(1)
		go func(queryName string, queryParams map[string]interface{}) {
			defer wg.Done()

			table := GetString(queryParams, "table")
			if table == "" {
				mu.Lock()
				results[queryName] = &QueryResult{Error: nil, Data: nil}
				mu.Unlock()
				return
			}

			// Remove table from params before passing to query
			delete(queryParams, "table")

			data, err := db.QueryCtx(ctx, table, queryParams)

			mu.Lock()
			results[queryName] = &QueryResult{Data: data, Error: err}
			mu.Unlock()
		}(name, params)
	}

	wg.Wait()
	return results
}

// ParallelQueryFuncs executes multiple query functions in parallel
func ParallelQueryFuncs(ctx context.Context, funcs map[string]func() (interface{}, error)) map[string]interface{} {
	results := make(map[string]interface{})
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, fn := range funcs {
		wg.Add(1)
		go func(queryName string, queryFn func() (interface{}, error)) {
			defer wg.Done()

			data, err := queryFn()

			mu.Lock()
			if err == nil {
				results[queryName] = data
			} else {
				results[queryName] = nil
			}
			mu.Unlock()
		}(name, fn)
	}

	wg.Wait()
	return results
}

// =============================================
// BATCH AGGREGATION HELPERS
// =============================================

// AggregateSum sums a float64 field across results
func AggregateSum(results []map[string]interface{}, field string) float64 {
	sum := 0.0
	for _, r := range results {
		sum += GetFloat64(r, field)
	}
	return sum
}

// AggregateCount counts results
func AggregateCount(results []map[string]interface{}) int {
	return len(results)
}

// AggregateSumInt sums an int field across results
func AggregateSumInt(results []map[string]interface{}, field string) int {
	sum := 0
	for _, r := range results {
		sum += GetInt(r, field)
	}
	return sum
}

// GroupBy groups results by a field
func GroupBy(results []map[string]interface{}, field string) map[string][]map[string]interface{} {
	groups := make(map[string][]map[string]interface{})
	for _, r := range results {
		key := GetString(r, field)
		groups[key] = append(groups[key], r)
	}
	return groups
}

// GroupBySum groups and sums by a field
func GroupBySum(results []map[string]interface{}, groupField, sumField string) map[string]float64 {
	groups := make(map[string]float64)
	for _, r := range results {
		key := GetString(r, groupField)
		groups[key] += GetFloat64(r, sumField)
	}
	return groups
}

// =============================================
// MAP BUILDING HELPERS (Optimized)
// =============================================

// BuildIDMap creates a map from ID to full object
func BuildIDMap(results []map[string]interface{}, idField string) map[string]map[string]interface{} {
	m := make(map[string]map[string]interface{}, len(results))
	for _, r := range results {
		id := GetString(r, idField)
		if id != "" {
			m[id] = r
		}
	}
	return m
}

// BuildIDNameMap creates a map from ID to name
func BuildIDNameMap(results []map[string]interface{}, idField, nameField string) map[string]string {
	m := make(map[string]string, len(results))
	for _, r := range results {
		id := GetString(r, idField)
		name := GetString(r, nameField)
		if id != "" {
			m[id] = name
		}
	}
	return m
}

// ExtractIDs extracts unique IDs from results
func ExtractIDs(results []map[string]interface{}, field string) []string {
	seen := make(map[string]struct{}, len(results))
	ids := make([]string, 0, len(results))
	for _, r := range results {
		id := GetString(r, field)
		if id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// =============================================
// SLICE PRE-ALLOCATION HELPERS
// =============================================

// MakeResults creates a pre-allocated slice for results
func MakeResults(capacity int) []map[string]interface{} {
	return make([]map[string]interface{}, 0, capacity)
}

// MakeStrings creates a pre-allocated string slice
func MakeStrings(capacity int) []string {
	return make([]string, 0, capacity)
}
