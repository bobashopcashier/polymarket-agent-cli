package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultRequestTimeout = 15 * time.Second
	defaultGammaMaxBytes  = int64(16 << 20)
	defaultCLOBMaxBytes   = int64(8 << 20)
	maxAllowedBodyBytes   = int64(64 << 20)
	maxErrorBodyBytes     = int64(64 << 10)
	defaultUserAgent      = "polymarket-agent-cli/0.1"
)

// Service identifies one of the fixed upstreams the CLI is allowed to call.
// It intentionally cannot carry a caller-provided base URL.
type Service uint8

const (
	Gamma Service = iota + 1
	CLOB
)

type serviceConfig struct {
	name     string
	baseURL  string
	maxBytes int64
}

// Options configures transport behavior. Hosts remain fixed regardless of the
// supplied HTTP client. HTTPClient is primarily useful for tests, proxies, and
// caller-owned observability transports.
type Options struct {
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	GammaMaxBytes  int64
	CLOBMaxBytes   int64
	UserAgent      string
}

// Client is a bounded HTTP client for Polymarket's public APIs.
type Client struct {
	httpClient     *http.Client
	requestTimeout time.Duration
	userAgent      string
	services       map[Service]serviceConfig
}

// New constructs a client that can only address the production Gamma and CLOB
// hosts. Redirects are not followed, which prevents a trusted endpoint from
// redirecting a request to an unapproved host.
func New(options Options) (*Client, error) {
	timeout := options.RequestTimeout
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	if timeout < 0 || timeout > time.Minute {
		return nil, fmt.Errorf("request timeout must be between 1ns and 1m")
	}

	gammaMax, err := resolveBodyLimit(options.GammaMaxBytes, defaultGammaMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("gamma response limit: %w", err)
	}
	clobMax, err := resolveBodyLimit(options.CLOBMaxBytes, defaultCLOBMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("clob response limit: %w", err)
	}

	httpClient := &http.Client{}
	if options.HTTPClient != nil {
		clone := *options.HTTPClient
		httpClient = &clone
	}
	if httpClient.Timeout == 0 || httpClient.Timeout > timeout {
		httpClient.Timeout = timeout
	}
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	if len(userAgent) > 128 || hasUnsafeText(userAgent) {
		return nil, fmt.Errorf("user agent contains unsafe characters or is too long")
	}

	return &Client{
		httpClient:     httpClient,
		requestTimeout: timeout,
		userAgent:      userAgent,
		services: map[Service]serviceConfig{
			Gamma: {name: "gamma", baseURL: "https://gamma-api.polymarket.com", maxBytes: gammaMax},
			CLOB:  {name: "clob", baseURL: "https://clob.polymarket.com", maxBytes: clobMax},
		},
	}, nil
}

func resolveBodyLimit(value, fallback int64) (int64, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < 1 || value > maxAllowedBodyBytes {
		return 0, fmt.Errorf("must be between 1 and %d bytes", maxAllowedBodyBytes)
	}
	return value, nil
}

// GetJSON performs a public GET and returns the upstream JSON without
// normalizing its shape. The body is validated as JSON and bounded by the
// configured service limit.
func (c *Client) GetJSON(ctx context.Context, service Service, path string, query url.Values) (json.RawMessage, error) {
	if ctx == nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "context is nil"}
	}
	config, ok := c.services[service]
	if !ok {
		return nil, &Error{Kind: ErrorInvalidRequest, Message: "unknown service"}
	}
	if err := validatePath(path); err != nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Service: config.name, Message: err.Error(), Cause: err}
	}

	endpoint, err := url.Parse(config.baseURL + path)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Service: config.name, Message: "could not construct request URL", Cause: err}
	}
	if len(query) != 0 {
		endpoint.RawQuery = query.Encode()
	}

	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidRequest, Service: config.name, Message: "could not construct request", Cause: err}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, classifyTransportError(config.name, http.MethodGet, path, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, readStatusError(config.name, http.MethodGet, path, response)
	}

	if response.ContentLength > config.maxBytes {
		return nil, &Error{
			Kind:       ErrorResponseTooLarge,
			Service:    config.name,
			Method:     http.MethodGet,
			Path:       path,
			StatusCode: response.StatusCode,
			Message:    fmt.Sprintf("response exceeds %d-byte limit", config.maxBytes),
		}
	}

	body, tooLarge, err := readBounded(response.Body, config.maxBytes)
	if err != nil {
		return nil, &Error{Kind: ErrorTransport, Service: config.name, Method: http.MethodGet, Path: path, StatusCode: response.StatusCode, Message: "could not read response body", Cause: err, Retryable: true}
	}
	if tooLarge {
		return nil, &Error{Kind: ErrorResponseTooLarge, Service: config.name, Method: http.MethodGet, Path: path, StatusCode: response.StatusCode, Message: fmt.Sprintf("response exceeds %d-byte limit", config.maxBytes)}
	}
	if !json.Valid(body) {
		return nil, &Error{Kind: ErrorInvalidResponse, Service: config.name, Method: http.MethodGet, Path: path, StatusCode: response.StatusCode, Message: "upstream returned invalid JSON"}
	}

	// Copy into a RawMessage with no spare capacity so callers cannot append into
	// a larger retained buffer.
	result := make(json.RawMessage, len(body))
	copy(result, body)
	return result, nil
}

func validatePath(path string) error {
	if path == "" || path[0] != '/' {
		return fmt.Errorf("path must begin with /")
	}
	if len(path) > 512 {
		return fmt.Errorf("path is too long")
	}
	if strings.Contains(path, "?") || strings.Contains(path, "#") || strings.Contains(path, "\\") {
		return fmt.Errorf("path must not contain query, fragment, or backslash characters")
	}
	if hasUnsafeText(path) {
		return fmt.Errorf("path contains unsafe characters")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("path traversal is not allowed")
		}
	}
	return nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	var buffer bytes.Buffer
	_, err := io.Copy(&buffer, io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(buffer.Len()) > limit {
		return nil, true, nil
	}
	return buffer.Bytes(), false, nil
}

func classifyTransportError(service, method, path string, err error) *Error {
	kind := ErrorTransport
	message := "request failed"
	retryable := true

	switch {
	case errors.Is(err, context.Canceled):
		kind = ErrorCanceled
		message = "request was canceled"
		retryable = false
	case errors.Is(err, context.DeadlineExceeded):
		kind = ErrorTimeout
		message = "request timed out"
	}

	return &Error{Kind: kind, Service: service, Method: method, Path: path, Message: message, Cause: err, Retryable: retryable}
}
