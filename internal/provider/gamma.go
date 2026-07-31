package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/httpx"
)

// Gamma provides public market and event discovery through the Gamma API.
type Gamma struct {
	http *httpx.Client
}

func NewGamma(client *httpx.Client) (*Gamma, error) {
	if client == nil {
		return nil, fmt.Errorf("gamma client requires a non-nil HTTP client")
	}
	return &Gamma{http: client}, nil
}

// ListMarketsRequest selects a page from Gamma's basic markets catalog.
type ListMarketsRequest struct {
	Limit     int
	Offset    int
	Active    *bool
	Closed    *bool
	Order     string
	Ascending *bool
}

func (g *Gamma) ListMarkets(ctx context.Context, request ListMarketsRequest) (json.RawMessage, error) {
	if err := validateLimit("limit", request.Limit, 500); err != nil {
		return nil, err
	}
	if err := validateOffset(request.Offset); err != nil {
		return nil, err
	}
	if err := validateOrder(request.Order); err != nil {
		return nil, err
	}

	query := url.Values{}
	addInt(query, "limit", request.Limit)
	addInt(query, "offset", request.Offset)
	addBool(query, "active", request.Active)
	addBool(query, "closed", request.Closed)
	addString(query, "order", request.Order)
	addBool(query, "ascending", request.Ascending)
	return g.http.GetJSON(ctx, httpx.Gamma, "/markets", query)
}

// ListMarketsKeysetRequest selects a stable cursor-paginated page. New
// integrations should prefer this over offset pagination when walking a large
// catalog.
type ListMarketsKeysetRequest struct {
	Limit       int
	AfterCursor string
	Closed      *bool
	Order       string
	Ascending   *bool
}

func (g *Gamma) ListMarketsKeyset(ctx context.Context, request ListMarketsKeysetRequest) (json.RawMessage, error) {
	if err := validateLimit("limit", request.Limit, 100); err != nil {
		return nil, err
	}
	if err := validateCursor(request.AfterCursor); err != nil {
		return nil, err
	}
	if err := validateOrder(request.Order); err != nil {
		return nil, err
	}
	query := url.Values{}
	addInt(query, "limit", request.Limit)
	addString(query, "after_cursor", request.AfterCursor)
	addBool(query, "closed", request.Closed)
	addString(query, "order", request.Order)
	addBool(query, "ascending", request.Ascending)
	return g.http.GetJSON(ctx, httpx.Gamma, "/markets/keyset", query)
}

func (g *Gamma) GetMarket(ctx context.Context, id string) (json.RawMessage, error) {
	if isDecimal(id) {
		if err := validatePositiveID("market_id", id); err != nil {
			return nil, err
		}
		return g.http.GetJSON(ctx, httpx.Gamma, "/markets/"+id, nil)
	}
	return g.GetMarketBySlug(ctx, id)
}

func (g *Gamma) GetMarketBySlug(ctx context.Context, slug string) (json.RawMessage, error) {
	if err := validateSlug("market_slug", slug); err != nil {
		return nil, err
	}
	return g.http.GetJSON(ctx, httpx.Gamma, "/markets/slug/"+url.PathEscape(slug), nil)
}

// ListEventsRequest selects a page from Gamma's basic events catalog.
type ListEventsRequest struct {
	Limit     int
	Offset    int
	Active    *bool
	Closed    *bool
	Order     string
	Ascending *bool
	TagSlug   string
}

func (g *Gamma) ListEvents(ctx context.Context, request ListEventsRequest) (json.RawMessage, error) {
	if err := validateLimit("limit", request.Limit, 500); err != nil {
		return nil, err
	}
	if err := validateOffset(request.Offset); err != nil {
		return nil, err
	}
	if err := validateOrder(request.Order); err != nil {
		return nil, err
	}
	if request.TagSlug != "" {
		if err := validateSlug("tag_slug", request.TagSlug); err != nil {
			return nil, err
		}
	}

	query := url.Values{}
	addInt(query, "limit", request.Limit)
	addInt(query, "offset", request.Offset)
	addBool(query, "active", request.Active)
	addBool(query, "closed", request.Closed)
	addString(query, "order", request.Order)
	addBool(query, "ascending", request.Ascending)
	addString(query, "tag_slug", request.TagSlug)
	return g.http.GetJSON(ctx, httpx.Gamma, "/events", query)
}

type ListEventsKeysetRequest struct {
	Limit       int
	AfterCursor string
	Closed      *bool
	Live        *bool
	Order       string
	Ascending   *bool
	TagSlug     string
}

func (g *Gamma) ListEventsKeyset(ctx context.Context, request ListEventsKeysetRequest) (json.RawMessage, error) {
	if err := validateLimit("limit", request.Limit, 500); err != nil {
		return nil, err
	}
	if err := validateCursor(request.AfterCursor); err != nil {
		return nil, err
	}
	if err := validateOrder(request.Order); err != nil {
		return nil, err
	}
	if request.TagSlug != "" {
		if err := validateSlug("tag_slug", request.TagSlug); err != nil {
			return nil, err
		}
	}
	query := url.Values{}
	addInt(query, "limit", request.Limit)
	addString(query, "after_cursor", request.AfterCursor)
	addBool(query, "closed", request.Closed)
	addBool(query, "live", request.Live)
	addString(query, "order", request.Order)
	addBool(query, "ascending", request.Ascending)
	addString(query, "tag_slug", request.TagSlug)
	return g.http.GetJSON(ctx, httpx.Gamma, "/events/keyset", query)
}

func (g *Gamma) GetEvent(ctx context.Context, id string) (json.RawMessage, error) {
	if isDecimal(id) {
		if err := validatePositiveID("event_id", id); err != nil {
			return nil, err
		}
		return g.http.GetJSON(ctx, httpx.Gamma, "/events/"+id, nil)
	}
	return g.GetEventBySlug(ctx, id)
}

func (g *Gamma) GetEventBySlug(ctx context.Context, slug string) (json.RawMessage, error) {
	if err := validateSlug("event_slug", slug); err != nil {
		return nil, err
	}
	return g.http.GetJSON(ctx, httpx.Gamma, "/events/slug/"+url.PathEscape(slug), nil)
}

type PublicSearchRequest struct {
	Query             string
	LimitPerType      int
	Page              int
	SearchTags        *bool
	SearchProfiles    *bool
	KeepClosedMarkets *bool
	Ascending         *bool
}

func (g *Gamma) PublicSearch(ctx context.Context, request PublicSearchRequest) (json.RawMessage, error) {
	queryText, err := validateSearchQuery(request.Query)
	if err != nil {
		return nil, err
	}
	if request.LimitPerType < 0 || request.LimitPerType > 100 {
		return nil, invalid("limit_per_type", "must be between 1 and 100 when provided")
	}
	if request.Page < 0 || request.Page > 10_000 {
		return nil, invalid("page", "must be between 1 and 10000 when provided")
	}

	query := url.Values{"q": {queryText}}
	addInt(query, "limit_per_type", request.LimitPerType)
	addInt(query, "page", request.Page)
	addBool(query, "search_tags", request.SearchTags)
	addBool(query, "search_profiles", request.SearchProfiles)
	addBool(query, "ascending", request.Ascending)
	if request.KeepClosedMarkets != nil {
		value := 0
		if *request.KeepClosedMarkets {
			value = 1
		}
		query.Set("keep_closed_markets", strconv.Itoa(value))
	}
	return g.http.GetJSON(ctx, httpx.Gamma, "/public-search", query)
}

func addString(query url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

func addInt(query url.Values, key string, value int) {
	if value != 0 {
		query.Set(key, strconv.Itoa(value))
	}
}

func addBool(query url.Values, key string, value *bool) {
	if value != nil {
		query.Set(key, strconv.FormatBool(*value))
	}
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
