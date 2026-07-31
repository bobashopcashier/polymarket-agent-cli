package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/httpx"
)

// CLOB provides unauthenticated price and order-book reads.
type CLOB struct {
	http *httpx.Client
}

func NewCLOB(client *httpx.Client) (*CLOB, error) {
	if client == nil {
		return nil, fmt.Errorf("clob client requires a non-nil HTTP client")
	}
	return &CLOB{http: client}, nil
}

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

func (c *CLOB) Health(ctx context.Context) (json.RawMessage, error) {
	return c.http.GetJSON(ctx, httpx.CLOB, "/", nil)
}

func (c *CLOB) ServerTime(ctx context.Context) (json.RawMessage, error) {
	return c.http.GetJSON(ctx, httpx.CLOB, "/time", nil)
}

func (c *CLOB) Price(ctx context.Context, tokenID string, side Side) (json.RawMessage, error) {
	query, err := tokenQuery(tokenID)
	if err != nil {
		return nil, err
	}
	side = Side(strings.ToUpper(string(side)))
	if side != SideBuy && side != SideSell {
		return nil, invalid("side", "must be BUY or SELL")
	}
	query.Set("side", string(side))
	return c.http.GetJSON(ctx, httpx.CLOB, "/price", query)
}

func (c *CLOB) Midpoint(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/midpoint", tokenID)
}

func (c *CLOB) Spread(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/spread", tokenID)
}

func (c *CLOB) Book(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/book", tokenID)
}

func (c *CLOB) LastTrade(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/last-trade-price", tokenID)
}

func (c *CLOB) TickSize(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/tick-size", tokenID)
}

func (c *CLOB) FeeRate(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/fee-rate", tokenID)
}

func (c *CLOB) NegRisk(ctx context.Context, tokenID string) (json.RawMessage, error) {
	return c.tokenRequest(ctx, "/neg-risk", tokenID)
}

func (c *CLOB) tokenRequest(ctx context.Context, path, tokenID string) (json.RawMessage, error) {
	query, err := tokenQuery(tokenID)
	if err != nil {
		return nil, err
	}
	return c.http.GetJSON(ctx, httpx.CLOB, path, query)
}

func tokenQuery(tokenID string) (url.Values, error) {
	if err := validateTokenID(tokenID); err != nil {
		return nil, err
	}
	return url.Values{"token_id": {tokenID}}, nil
}
