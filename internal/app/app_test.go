package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func newTestApp(t *testing.T, transport http.RoundTripper, stdin string) (*App, *bytes.Buffer, *bytes.Buffer, *atomic.Int32) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	calls := &atomic.Int32{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if transport == nil {
			t.Fatalf("unexpected network request: %s", request.URL)
		}
		return transport.RoundTrip(request)
	})}
	application, err := New(Dependencies{Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	return application, &stdout, &stderr, calls
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)),
	}
}

func decodeDocument(t *testing.T, source *bytes.Buffer) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(source.Bytes()))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, source.String())
	}
	return document
}

func TestSchemaIsOfflineAndDeterministic(t *testing.T) {
	application, stdout, stderr, calls := newTestApp(t, nil, "")
	if exit := application.Run(context.Background(), []string{"schema", "markets.list", "--compact"}); exit != 0 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
	}
	if calls.Load() != 0 || stderr.Len() != 0 {
		t.Fatalf("schema made network calls or wrote stderr: calls=%d stderr=%s", calls.Load(), stderr.String())
	}
	document := decodeDocument(t, stdout)
	if document["schemaVersion"] != "pmx.command-schema/v1" || document["id"] != "markets.list" {
		t.Fatalf("unexpected schema: %#v", document)
	}
}

func TestExplicitJSONOutputControl(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		wantExit  int
		wantCode  string
	}{
		{name: "output json", arguments: []string{"schema", "markets.list", "--output", "json", "--compact"}, wantExit: 0},
		{name: "output json inline", arguments: []string{"schema", "markets.list", "--output=json", "--compact"}, wantExit: 0},
		{name: "reject unsupported format", arguments: []string{"schema", "--output", "table", "--compact"}, wantExit: 2, wantCode: "invalid_output_format"},
		{name: "reject alias conflict", arguments: []string{"schema", "--output", "json", "--json", "--compact"}, wantExit: 2, wantCode: "conflicting_inputs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, stdout, stderr, calls := newTestApp(t, nil, "")
			if exit := application.Run(context.Background(), test.arguments); exit != test.wantExit {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", exit, test.wantExit, stdout.String(), stderr.String())
			}
			if calls.Load() != 0 {
				t.Fatalf("output control made %d network calls", calls.Load())
			}
			if test.wantCode == "" {
				if stdout.Len() == 0 || stderr.Len() != 0 {
					t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
				}
				return
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid output control wrote stdout: %s", stdout.String())
			}
			document := decodeDocument(t, stderr)
			if document["error"].(map[string]any)["code"] != test.wantCode {
				t.Fatalf("unexpected error: %#v", document)
			}
		})
	}
}

func TestDryRunControlRejectsNonMutationsAndExecuteConflict(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		wantCode  string
	}{
		{name: "read command", arguments: []string{"markets", "list", "--dry-run", "--compact"}, wantCode: "dry_run_not_supported"},
		{name: "execute conflict", arguments: []string{"wallet", "create", "--dry-run", "--execute", "--params", `{"name":"conflict"}`, "--compact"}, wantCode: "conflicting_inputs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, stdout, stderr, calls := newTestApp(t, nil, "")
			if exit := application.Run(context.Background(), test.arguments); exit != 2 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 || calls.Load() != 0 {
				t.Fatalf("invalid dry-run crossed a boundary: stdout=%s calls=%d", stdout.String(), calls.Load())
			}
			document := decodeDocument(t, stderr)
			if document["error"].(map[string]any)["code"] != test.wantCode {
				t.Fatalf("unexpected error: %#v", document)
			}
		})
	}
}

func TestEveryCommandSchemaIncludesAnExample(t *testing.T) {
	commandRegistry, err := newCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commandRegistry.Commands() {
		schema, ok := commandRegistry.Schema(command.Path...)
		if !ok {
			t.Fatalf("schema not found for %s", command.ID)
		}
		if len(schema.Examples) == 0 {
			t.Errorf("schema %s has no examples", command.ID)
		}
		controls := map[string]bool{}
		for _, control := range schema.InvocationControls {
			controls[control.Name] = true
		}
		outputControls := map[string]bool{}
		for _, control := range schema.Params.OutputControls {
			outputControls[control] = true
		}
		for _, required := range []string{"--output", "--json", "--compact"} {
			if !controls[required] || !outputControls[required] {
				t.Errorf("schema %s omitted output control %s", command.ID, required)
			}
		}
		mutation := schema.Effects.Effects.IsMutation()
		for _, mutationControl := range []string{"--dry-run", "--execute"} {
			if controls[mutationControl] != mutation || outputControls[mutationControl] != mutation {
				t.Errorf("schema %s has inaccurate mutation control %s", command.ID, mutationControl)
			}
		}
		projectable := len(schema.Output.ResponseFields) != 0
		if controls["--fields"] != projectable || outputControls["--fields"] != projectable {
			t.Errorf("schema %s has inaccurate --fields discoverability", command.ID)
		}
	}
}

func TestRawParamsAndProjectionPreserveEnvelope(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "gamma-api.polymarket.com" || request.URL.Path != "/markets" {
			t.Fatalf("unexpected request URL: %s", request.URL)
		}
		if got := request.URL.Query().Get("limit"); got != "1" {
			t.Fatalf("limit = %q", got)
		}
		return jsonResponse(200, `[{"id":"1","question":"Will it work?","description":"omit"}]`), nil
	})
	application, stdout, stderr, calls := newTestApp(t, transport, "")
	exit := application.Run(context.Background(), []string{
		"markets", "list", "--params", `{"active":true,"limit":1}`, "--fields", "id,question", "--compact",
	})
	if exit != 0 || stderr.Len() != 0 || calls.Load() != 1 {
		t.Fatalf("exit=%d calls=%d stderr=%s", exit, calls.Load(), stderr.String())
	}
	document := decodeDocument(t, stdout)
	if document["meta"] == nil || document["schemaVersion"] != "pmx.response/v1" {
		t.Fatalf("missing envelope metadata: %#v", document)
	}
	data := document["data"].([]any)[0].(map[string]any)
	if len(data) != 2 || data["id"] != "1" || data["question"] != "Will it work?" {
		t.Fatalf("unexpected projection: %#v", data)
	}
	pagination := document["meta"].(map[string]any)["pagination"].(map[string]any)
	if pagination["complete"] != false {
		t.Fatalf("full page must be conservatively incomplete: %#v", pagination)
	}
}

func TestSchemaAdvertisedTokenFlagWorks(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("token_id") != "123" {
			t.Fatalf("token_id = %q", request.URL.Query().Get("token_id"))
		}
		return jsonResponse(200, `{"mid":"0.5"}`), nil
	})
	application, stdout, stderr, calls := newTestApp(t, transport, "")
	if exit := application.Run(context.Background(), []string{"clob", "midpoint", "--token-id", "123", "--compact"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if calls.Load() != 1 || stdout.Len() == 0 {
		t.Fatalf("calls=%d stdout=%s", calls.Load(), stdout.String())
	}
}

func TestInvalidInputsNeverReachNetwork(t *testing.T) {
	tests := [][]string{
		{"markets", "list", "--params", `{"limit":1,"limit":2}`},
		{"markets", "list", "--params", `{"limit":1}`, "--limit", "2"},
		{"clob", "book", "123?side=BUY"},
		{"markets", "get", "abc?closed=true"},
		{"markets", "get", "%2e%2e"},
		{"markets", "get", "../secret"},
		{"markets", "get", "bad\nslug"},
		{"markets", "get", "--params", `{"id":"../secret"}`},
		{"markets", "list", "--fields", "definitely_missing"},
	}
	for _, arguments := range tests {
		t.Run(strings.Join(arguments[:2], "_"), func(t *testing.T) {
			application, stdout, stderr, calls := newTestApp(t, nil, "")
			if exit := application.Run(context.Background(), arguments); exit != 2 {
				t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
			}
			if stdout.Len() != 0 || calls.Load() != 0 {
				t.Fatalf("invalid input produced stdout or network: stdout=%s calls=%d", stdout.String(), calls.Load())
			}
			document := decodeDocument(t, stderr)
			if document["schemaVersion"] != "pmx.error/v1" || document["ok"] != false {
				t.Fatalf("unexpected error envelope: %#v", document)
			}
		})
	}
}

func TestSearchBoundsNestedMarketsAndUsesProviderPagination(t *testing.T) {
	markets := make([]map[string]string, 101)
	for index := range markets {
		markets[index] = map[string]string{"id": fmt.Sprintf("%d", index+1)}
	}
	payload, err := json.Marshal(map[string]any{
		"events":     []any{map[string]any{"id": "event-1", "markets": markets}},
		"pagination": map[string]any{"hasMore": true, "totalResults": 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, string(payload)), nil })
	application, stdout, stderr, _ := newTestApp(t, transport, "")
	if exit := application.Run(context.Background(), []string{"markets", "search", "bitcoin", "--compact"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	document := decodeDocument(t, stdout)
	events := document["data"].(map[string]any)["events"].([]any)
	if got := len(events[0].(map[string]any)["markets"].([]any)); got != 100 {
		t.Fatalf("nested markets = %d", got)
	}
	meta := document["meta"].(map[string]any)
	if meta["pagination"].(map[string]any)["complete"] != false || len(meta["truncation"].([]any)) != 1 {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
}

func TestCommandProviderByteLimitIsEnforced(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		response := jsonResponse(200, `{"minimum_tick_size":"0.01"}`)
		response.ContentLength = (1 << 20) + 1
		return response, nil
	})
	application, stdout, stderr, calls := newTestApp(t, transport, "")
	if exit := application.Run(context.Background(), []string{"clob", "tick-size", "123", "--compact"}); exit != 6 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 || calls.Load() != 1 {
		t.Fatalf("stdout=%s calls=%d", stdout.String(), calls.Load())
	}
	document := decodeDocument(t, stderr)
	if document["error"].(map[string]any)["code"] != "provider_response_too_large" {
		t.Fatalf("unexpected error: %#v", document)
	}
}

func TestRateLimitIsRetryableExitSeven(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		response := jsonResponse(429, `{"error":"slow down"}`)
		response.Header.Set("Retry-After", "3")
		return response, nil
	})
	application, stdout, stderr, calls := newTestApp(t, transport, "")
	if exit := application.Run(context.Background(), []string{"markets", "list", "--limit", "1", "--compact"}); exit != 7 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 || calls.Load() != 1 {
		t.Fatalf("stdout=%s calls=%d", stdout.String(), calls.Load())
	}
	document := decodeDocument(t, stderr)
	errorDocument := document["error"].(map[string]any)
	if errorDocument["code"] != "rate_limited" || errorDocument["retryable"] != true {
		t.Fatalf("unexpected error: %#v", errorDocument)
	}
}

func TestOrderBookDepthIsTruncatedWithMetadata(t *testing.T) {
	levels := make([]map[string]string, 205)
	for index := range levels {
		levels[index] = map[string]string{"price": "0.5", "size": fmt.Sprintf("%d", index+1)}
	}
	payload, err := json.Marshal(map[string]any{"market": "0x1", "asset_id": "123", "bids": levels, "asks": levels})
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, string(payload)), nil })
	application, stdout, stderr, _ := newTestApp(t, transport, "")
	if exit := application.Run(context.Background(), []string{"clob", "book", "123", "--compact"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	document := decodeDocument(t, stdout)
	data := document["data"].(map[string]any)
	if len(data["bids"].([]any)) != 200 || len(data["asks"].([]any)) != 200 {
		t.Fatalf("order-book depth was not bounded")
	}
	meta := document["meta"].(map[string]any)
	if len(meta["truncation"].([]any)) != 2 {
		t.Fatalf("missing truncation metadata: %#v", meta)
	}
}

func TestProviderStringsAreSanitized(t *testing.T) {
	hostile := "hello\x1b[31m\u202eworld"
	payload, err := json.Marshal([]map[string]string{{"id": "1", "question": hostile}})
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, string(payload)), nil })
	application, stdout, stderr, _ := newTestApp(t, transport, "")
	if exit := application.Run(context.Background(), []string{"markets", "list", "--limit", "1", "--compact"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte{0x1b}) || strings.ContainsRune(stdout.String(), '\u202e') {
		t.Fatalf("unsafe rune remained in JSON output: %q", stdout.String())
	}
	document := decodeDocument(t, stdout)
	question := document["data"].([]any)[0].(map[string]any)["question"].(string)
	if !strings.Contains(question, `\u001B`) || !strings.Contains(question, `\u202E`) {
		t.Fatalf("sanitization was not explicit: %q", question)
	}
}
