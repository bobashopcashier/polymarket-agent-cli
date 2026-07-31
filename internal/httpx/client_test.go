package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper

	mu           sync.Mutex
	originalHost string
}

func (transport *rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.originalHost = request.URL.Host
	transport.mu.Unlock()

	clone := request.Clone(request.Context())
	clone.URL.Scheme = transport.target.Scheme
	clone.URL.Host = transport.target.Host
	clone.Host = transport.target.Host
	return transport.base.RoundTrip(clone)
}

func (transport *rewriteTransport) host() string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.originalHost
}

func newTestClient(t *testing.T, handler http.Handler, options Options) (*Client, *rewriteTransport) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &rewriteTransport{target: target, base: http.DefaultTransport}
	options.HTTPClient = &http.Client{Transport: transport}
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return client, transport
}

func TestGetJSONUsesFixedHostAndEncodesQuery(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/markets/keyset" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.URL.Query().Get("title"); got != "rate cuts & jobs" {
			t.Errorf("title = %q", got)
		}
		if got := request.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"items":[{"new_field":true}]}`)
	})
	client, transport := newTestClient(t, handler, Options{})

	message, err := client.GetJSON(context.Background(), Gamma, "/markets/keyset", url.Values{"title": {"rate cuts & jobs"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(message) != `{"items":[{"new_field":true}]}` {
		t.Fatalf("raw response changed: %s", message)
	}
	if got := transport.host(); got != "gamma-api.polymarket.com" {
		t.Fatalf("original host = %q", got)
	}
}

func TestGetJSONRejectsOversizedBody(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"value":"too large"}`)
	}), Options{GammaMaxBytes: 8})

	_, err := client.GetJSON(context.Background(), Gamma, "/markets/keyset", nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorResponseTooLarge {
		t.Fatalf("error = %#v", err)
	}
}

func TestGetJSONClassifiesAndSanitizesRateLimit(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Retry-After", "7")
		writer.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(writer, "{\"error\":\"slow\\u001b[31m down\\u202e\",\"code\":\"rate_limit!\"}")
	}), Options{})

	_, err := client.GetJSON(context.Background(), CLOB, "/book", nil)
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("error = %#v", err)
	}
	if typed.Kind != ErrorRateLimited || !typed.Retryable || typed.RetryAfter != 7*time.Second {
		t.Fatalf("classification = %#v", typed)
	}
	if typed.Code != "rate_limit" || strings.ContainsRune(typed.Message, '\u001b') || strings.ContainsRune(typed.Message, '\u202e') {
		t.Fatalf("unsanitized error = %#v", typed)
	}
}

func TestStatusClassificationDistinguishesTransientFailures(t *testing.T) {
	tests := []struct {
		status    int
		kind      ErrorKind
		retryable bool
	}{
		{status: http.StatusRequestTimeout, kind: ErrorTimeout, retryable: true},
		{status: http.StatusTooEarly, kind: ErrorRejected, retryable: true},
		{status: http.StatusTooManyRequests, kind: ErrorRateLimited, retryable: true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			kind, retryable := classifyStatus(test.status)
			if kind != test.kind || retryable != test.retryable {
				t.Fatalf("classification = %s, %t", kind, retryable)
			}
		})
	}
}

func TestGetJSONRejectsInvalidJSON(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, "not-json")
	}), Options{})

	_, err := client.GetJSON(context.Background(), CLOB, "/time", nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorInvalidResponse {
		t.Fatalf("error = %#v", err)
	}
}

func TestGetJSONTimesOut(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(writer, `"OK"`)
	}), Options{RequestTimeout: 10 * time.Millisecond})

	_, err := client.GetJSON(context.Background(), CLOB, "/", nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorTimeout {
		t.Fatalf("error = %#v", err)
	}
}

func TestGetJSONDoesNotFollowRedirects(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://example.invalid/steal", http.StatusFound)
	}), Options{})

	_, err := client.GetJSON(context.Background(), Gamma, "/markets/keyset", nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorRejected || typed.StatusCode != http.StatusFound {
		t.Fatalf("error = %#v", err)
	}
}

func TestGetJSONRejectsUnsafePaths(t *testing.T) {
	client, _ := newTestClient(t, http.NotFoundHandler(), Options{})
	for _, path := range []string{"https://example.com", "/../secret", "/book?token=x", "/book\u202e"} {
		t.Run(path, func(t *testing.T) {
			_, err := client.GetJSON(context.Background(), CLOB, path, nil)
			var typed *Error
			if !errors.As(err, &typed) || typed.Kind != ErrorInvalidRequest {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestNewValidatesBounds(t *testing.T) {
	if _, err := New(Options{RequestTimeout: time.Minute + time.Nanosecond}); err == nil {
		t.Fatal("expected timeout validation error")
	}
	if _, err := New(Options{GammaMaxBytes: maxAllowedBodyBytes + 1}); err == nil {
		t.Fatal("expected response limit validation error")
	}
}
