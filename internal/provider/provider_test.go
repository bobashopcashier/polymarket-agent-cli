package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/httpx"
)

type rewriteTransport struct {
	target *url.URL
}

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = transport.target.Scheme
	clone.URL.Host = transport.target.Host
	clone.Host = transport.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func testHTTPClient(t *testing.T, handler http.Handler) *httpx.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := httpx.New(httpx.Options{HTTPClient: &http.Client{Transport: rewriteTransport{target: target}}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func boolPointer(value bool) *bool { return &value }

func TestGammaListMarketsUsesBasicCatalogEndpoint(t *testing.T) {
	client := testHTTPClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/markets" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("limit") != "25" || query.Get("offset") != "50" || query.Get("active") != "true" || query.Get("closed") != "false" || query.Get("order") != "volume_num,liquidity_num" || query.Get("ascending") != "true" {
			t.Fatalf("query = %v", query)
		}
		fmt.Fprint(writer, `[{"unknown":"preserved"}]`)
	}))
	gamma, err := NewGamma(client)
	if err != nil {
		t.Fatal(err)
	}

	result, err := gamma.ListMarkets(context.Background(), ListMarketsRequest{
		Limit: 25, Offset: 50, Active: boolPointer(true), Closed: boolPointer(false),
		Order: "volume_num,liquidity_num", Ascending: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `[{"unknown":"preserved"}]` {
		t.Fatalf("result = %s", result)
	}
}

func TestGammaListEventsUsesBasicCatalogEndpoint(t *testing.T) {
	client := testHTTPClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/events" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("limit") != "20" || query.Get("offset") != "40" || query.Get("active") != "true" || query.Get("closed") != "false" || query.Get("order") != "startDate" || query.Get("ascending") != "false" || query.Get("tag_slug") != "politics" {
			t.Fatalf("query = %v", query)
		}
		fmt.Fprint(writer, `[]`)
	}))
	gamma, err := NewGamma(client)
	if err != nil {
		t.Fatal(err)
	}

	_, err = gamma.ListEvents(context.Background(), ListEventsRequest{
		Limit: 20, Offset: 40, Active: boolPointer(true), Closed: boolPointer(false),
		Order: "startDate", Ascending: boolPointer(false), TagSlug: "politics",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGammaKeysetCatalogEndpoints(t *testing.T) {
	wanted := []string{"/markets/keyset", "/events/keyset"}
	var index atomic.Int32
	client := testHTTPClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		position := int(index.Add(1) - 1)
		if request.URL.Path != wanted[position] || request.URL.Query().Get("after_cursor") != "opaque+/cursor" {
			t.Fatalf("request = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		fmt.Fprint(writer, `{}`)
	}))
	gamma, _ := NewGamma(client)
	if _, err := gamma.ListMarketsKeyset(context.Background(), ListMarketsKeysetRequest{AfterCursor: "opaque+/cursor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.ListEventsKeyset(context.Background(), ListEventsKeysetRequest{AfterCursor: "opaque+/cursor"}); err != nil {
		t.Fatal(err)
	}
}

func TestGammaGetAndSearchPaths(t *testing.T) {
	wanted := []string{
		"/markets/42", "/markets/slug/rate-cuts-2026", "/markets/slug/another-market",
		"/events/7", "/events/slug/world-cup", "/events/slug/another-event", "/public-search",
	}
	var index atomic.Int32
	client := testHTTPClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		position := int(index.Add(1) - 1)
		if position >= len(wanted) || request.URL.Path != wanted[position] {
			t.Fatalf("request %d path = %s", position, request.URL.Path)
		}
		if request.URL.Path == "/public-search" {
			if request.URL.Query().Get("q") != "rates & jobs" || request.URL.Query().Get("keep_closed_markets") != "1" {
				t.Fatalf("search query = %v", request.URL.Query())
			}
		}
		fmt.Fprint(writer, `{}`)
	}))
	gamma, _ := NewGamma(client)
	if _, err := gamma.GetMarket(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.GetMarketBySlug(context.Background(), "rate-cuts-2026"); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.GetMarket(context.Background(), "another-market"); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.GetEvent(context.Background(), "7"); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.GetEventBySlug(context.Background(), "world-cup"); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.GetEvent(context.Background(), "another-event"); err != nil {
		t.Fatal(err)
	}
	if _, err := gamma.PublicSearch(context.Background(), PublicSearchRequest{Query: " rates & jobs ", KeepClosedMarkets: boolPointer(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestGammaValidationStopsRequest(t *testing.T) {
	var requests atomic.Int32
	client := testHTTPClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(writer, `{}`)
	}))
	gamma, _ := NewGamma(client)

	cases := []func() error{
		func() error {
			_, err := gamma.ListMarkets(context.Background(), ListMarketsRequest{Limit: 501})
			return err
		},
		func() error { _, err := gamma.GetMarket(context.Background(), "../1"); return err },
		func() error { _, err := gamma.GetEventBySlug(context.Background(), "bad/slug"); return err },
		func() error {
			_, err := gamma.PublicSearch(context.Background(), PublicSearchRequest{Query: "bad\u202equery"})
			return err
		},
	}
	for _, run := range cases {
		var validation *ValidationError
		if err := run(); !errors.As(err, &validation) {
			t.Fatalf("error = %#v", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("made %d requests", requests.Load())
	}
}

func TestCLOBPublicEndpoints(t *testing.T) {
	tokenID := "107505882767731489358349912513945399560393482969656700824895970500493757150417"
	wantedPaths := []string{"/", "/time", "/price", "/midpoint", "/spread", "/book", "/last-trade-price", "/tick-size", "/fee-rate", "/neg-risk"}
	var index atomic.Int32
	client := testHTTPClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		position := int(index.Add(1) - 1)
		if position >= len(wantedPaths) || request.URL.Path != wantedPaths[position] {
			t.Fatalf("request %d path = %s", position, request.URL.Path)
		}
		if position >= 2 && request.URL.Query().Get("token_id") != tokenID {
			t.Fatalf("token_id = %q", request.URL.Query().Get("token_id"))
		}
		if request.URL.Path == "/price" && request.URL.Query().Get("side") != "BUY" {
			t.Fatalf("side = %q", request.URL.Query().Get("side"))
		}
		fmt.Fprint(writer, `{}`)
	}))
	clob, _ := NewCLOB(client)

	calls := []func() error{
		func() error { _, err := clob.Health(context.Background()); return err },
		func() error { _, err := clob.ServerTime(context.Background()); return err },
		func() error { _, err := clob.Price(context.Background(), tokenID, Side("buy")); return err },
		func() error { _, err := clob.Midpoint(context.Background(), tokenID); return err },
		func() error { _, err := clob.Spread(context.Background(), tokenID); return err },
		func() error { _, err := clob.Book(context.Background(), tokenID); return err },
		func() error { _, err := clob.LastTrade(context.Background(), tokenID); return err },
		func() error { _, err := clob.TickSize(context.Background(), tokenID); return err },
		func() error { _, err := clob.FeeRate(context.Background(), tokenID); return err },
		func() error { _, err := clob.NegRisk(context.Background(), tokenID); return err },
	}
	for _, call := range calls {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCLOBValidatesTokenAndSide(t *testing.T) {
	client := testHTTPClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("request should not be made")
	}))
	clob, _ := NewCLOB(client)

	for _, token := range []string{"", "0", "-1", "0x123", "1?side=SELL"} {
		_, err := clob.Book(context.Background(), token)
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("token %q error = %#v", token, err)
		}
	}
	_, err := clob.Price(context.Background(), "1", Side("hold"))
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "side" {
		t.Fatalf("side error = %#v", err)
	}
}

func TestConstructorsRejectNilTransport(t *testing.T) {
	if _, err := NewGamma(nil); err == nil {
		t.Fatal("expected gamma constructor error")
	}
	if _, err := NewCLOB(nil); err == nil {
		t.Fatal("expected clob constructor error")
	}
}
