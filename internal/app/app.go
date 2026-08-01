package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/httpx"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/output"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/provider"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/sanitize"
)

type Dependencies struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	HTTPClient *http.Client
}

type App struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	httpClient *http.Client
	commands   *registry.Registry
}

func New(dependencies Dependencies) (*App, error) {
	commands, err := newCommandRegistry()
	if err != nil {
		return nil, err
	}
	if dependencies.Stdin == nil {
		dependencies.Stdin = os.Stdin
	}
	if dependencies.Stdout == nil {
		dependencies.Stdout = os.Stdout
	}
	if dependencies.Stderr == nil {
		dependencies.Stderr = os.Stderr
	}
	return &App{
		stdin: dependencies.Stdin, stdout: dependencies.Stdout, stderr: dependencies.Stderr,
		httpClient: dependencies.HTTPClient, commands: commands,
	}, nil
}

func (a *App) Run(ctx context.Context, arguments []string) int {
	options, remaining, parseErr := parseGlobal(arguments)
	writer := output.NewWriter(a.stdout, a.stderr, output.Options{
		Format: output.FormatJSON, Compact: options.Compact, Fields: options.Fields,
		MaximumBytes: output.DefaultMaximumBytes,
	})
	if parseErr != nil {
		return a.fail(writer, "", parseErr)
	}
	if options.Version || len(remaining) == 1 && remaining[0] == "version" {
		if err := output.WriteJSON(a.stdout, map[string]any{"name": "pmx", "version": version}, options.Compact, output.DefaultMaximumBytes); err != nil {
			return a.fail(writer, "version", err)
		}
		return 0
	}
	if options.Help || len(remaining) == 0 || len(remaining) == 1 && remaining[0] == "help" {
		if _, err := io.WriteString(a.stdout, helpText()); err != nil {
			return a.fail(writer, "help", contract.Internal("could not write help", err))
		}
		return 0
	}
	if remaining[0] == "schema" {
		if options.HasParams || options.Fields != "" {
			return a.fail(writer, "schema", contract.Invalid("conflicting_inputs", "schema does not accept --params or --fields"))
		}
		return a.runSchema(writer, remaining[1:])
	}

	invocation, err := parseInvocation(arguments, a.stdin, a.commands)
	if err != nil {
		return a.fail(writer, commandHint(remaining), err)
	}
	if invocation.Options.Fields != "" {
		if err := output.ValidateFieldMask(invocation.Options.Fields, invocation.Command.Output.ResponseFields); err != nil {
			return a.fail(writer, invocation.Command.ID, err)
		}
	}
	writer.Options.MaximumBytes = invocation.Command.Output.MaximumEncodedOutputBytes
	data, meta, err := a.execute(ctx, invocation)
	if err != nil {
		return a.fail(writer, invocation.Command.ID, mapExecutionError(err))
	}
	if err := writer.Success(contract.Success(invocation.Command.ID, data, meta), invocation.Command.Output.ResponseFields); err != nil {
		return a.fail(writer, invocation.Command.ID, err)
	}
	return 0
}

func (a *App) runSchema(writer *output.Writer, path []string) int {
	if len(path) == 0 {
		if err := output.WriteJSON(a.stdout, a.commands.Index(), writer.Options.Compact, output.DefaultMaximumBytes); err != nil {
			return a.fail(writer, "schema", err)
		}
		return 0
	}
	if len(path) > 2 {
		return a.fail(writer, "schema", contract.Invalid("invalid_schema_path", "schema accepts a command group or one leaf command"))
	}
	if schema, ok := a.commands.Schema(path...); ok {
		if err := output.WriteJSON(a.stdout, schema, writer.Options.Compact, output.DefaultMaximumBytes); err != nil {
			return a.fail(writer, "schema", err)
		}
		return 0
	}
	index := a.commands.Index(path...)
	if len(index.Commands) == 0 {
		return a.fail(writer, "schema", contract.Invalid("unknown_command", fmt.Sprintf("no command schema matches %q", strings.Join(path, " "))))
	}
	if err := output.WriteJSON(a.stdout, index, writer.Options.Compact, output.DefaultMaximumBytes); err != nil {
		return a.fail(writer, "schema", err)
	}
	return 0
}

func (a *App) execute(ctx context.Context, invocation invocation) (any, contract.Meta, error) {
	providerLimit := invocation.Command.Output.MaximumProviderBytes
	client, err := httpx.New(httpx.Options{
		HTTPClient: a.httpClient, RequestTimeout: invocation.Options.Timeout,
		GammaMaxBytes: providerLimit, CLOBMaxBytes: providerLimit,
	})
	if err != nil {
		return nil, contract.Meta{}, contract.Internal("could not configure Polymarket transport", err)
	}
	gamma, err := provider.NewGamma(client)
	if err != nil {
		return nil, contract.Meta{}, contract.Internal("could not configure Gamma client", err)
	}
	clob, err := provider.NewCLOB(client)
	if err != nil {
		return nil, contract.Meta{}, contract.Internal("could not configure CLOB client", err)
	}
	var raw json.RawMessage
	providerName := "polymarket-gamma"
	switch invocation.Command.ID {
	case "doctor":
		marketPage, gammaErr := gamma.ListMarkets(ctx, provider.ListMarketsRequest{Limit: 1})
		health, clobErr := clob.Health(ctx)
		if gammaErr != nil {
			return nil, contract.Meta{}, gammaErr
		}
		if clobErr != nil {
			return nil, contract.Meta{}, clobErr
		}
		gammaData, err := decodeRaw(marketPage)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		clobData, err := decodeRaw(health)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		data := map[string]any{
			"ready":          true,
			"gamma":          map[string]any{"reachable": true, "sampleItems": collectionCount(gammaData)},
			"clob":           map[string]any{"reachable": true, "status": clobData},
			"authentication": map[string]any{"required": false, "configured": false, "tradingEnabled": false},
		}
		safeData, err := sanitize.Value(data)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		return safeData, readMeta("polymarket"), nil
	case "markets.list":
		request := invocation.Request.(*marketsListRequest)
		raw, err = gamma.ListMarkets(ctx, provider.ListMarketsRequest{
			Limit: request.Limit, Offset: request.Offset, Active: request.Active, Closed: request.Closed,
			Order: request.Order, Ascending: request.Ascending,
		})
	case "markets.get":
		request := invocation.Request.(*resourceRequest)
		if numeric(request.ID) {
			raw, err = gamma.GetMarket(ctx, request.ID)
		} else {
			raw, err = gamma.GetMarketBySlug(ctx, request.ID)
		}
	case "markets.search":
		request := invocation.Request.(*searchRequest)
		raw, err = gamma.PublicSearch(ctx, provider.PublicSearchRequest{Query: request.Query, LimitPerType: request.Limit})
	case "events.list":
		request := invocation.Request.(*eventsListRequest)
		raw, err = gamma.ListEvents(ctx, provider.ListEventsRequest{
			Limit: request.Limit, Offset: request.Offset, Active: request.Active, Closed: request.Closed,
			Order: request.Order, Ascending: request.Ascending, TagSlug: request.Tag,
		})
	case "events.get":
		request := invocation.Request.(*resourceRequest)
		if numeric(request.ID) {
			raw, err = gamma.GetEvent(ctx, request.ID)
		} else {
			raw, err = gamma.GetEventBySlug(ctx, request.ID)
		}
	case "clob.price":
		providerName = "polymarket-clob"
		request := invocation.Request.(*priceRequest)
		raw, err = clob.Price(ctx, request.TokenID, provider.Side(request.Side))
	case "clob.midpoint", "clob.spread", "clob.book", "clob.tick-size", "clob.fee-rate", "clob.neg-risk", "clob.last-trade":
		providerName = "polymarket-clob"
		request := invocation.Request.(*tokenRequest)
		switch invocation.Command.ID {
		case "clob.midpoint":
			raw, err = clob.Midpoint(ctx, request.TokenID)
		case "clob.spread":
			raw, err = clob.Spread(ctx, request.TokenID)
		case "clob.book":
			raw, err = clob.Book(ctx, request.TokenID)
		case "clob.tick-size":
			raw, err = clob.TickSize(ctx, request.TokenID)
		case "clob.fee-rate":
			raw, err = clob.FeeRate(ctx, request.TokenID)
		case "clob.neg-risk":
			raw, err = clob.NegRisk(ctx, request.TokenID)
		case "clob.last-trade":
			raw, err = clob.LastTrade(ctx, request.TokenID)
		}
	case "clob.time":
		providerName = "polymarket-clob"
		raw, err = clob.ServerTime(ctx)
	default:
		return nil, contract.Meta{}, contract.Internal("command has no execution handler", nil)
	}
	if err != nil {
		return nil, contract.Meta{}, err
	}
	data, err := decodeRaw(raw)
	if err != nil {
		return nil, contract.Meta{}, err
	}
	data, err = sanitize.Value(data)
	if err != nil {
		return nil, contract.Meta{}, err
	}
	meta := readMeta(providerName)
	data, meta.Truncation = boundResult(invocation.Command, data)
	if invocation.Command.Output.Collection {
		meta.Pagination = &contract.Pagination{
			ItemsEmitted: collectionCount(data), Complete: collectionComplete(invocation, data), PagesFetched: 1,
		}
	}
	return data, meta, nil
}

func boundResult(command registry.CommandSpec, value any) (any, []contract.Truncation) {
	limit := command.Output.HardItemLimit
	if limit <= 0 {
		return value, nil
	}
	truncation := make([]contract.Truncation, 0, 2)
	if items, ok := value.([]any); ok && len(items) > limit {
		sourceCount := len(items)
		return items[:limit], []contract.Truncation{{Path: "data", Reason: "item_limit", SourceCount: &sourceCount, EmittedCount: limit}}
	}
	if command.ID != "clob.book" {
		if command.ID != "markets.search" {
			return value, nil
		}
		result, ok := value.(map[string]any)
		if !ok {
			return value, nil
		}
		events, ok := result["events"].([]any)
		if !ok {
			return value, nil
		}
		truncation := make([]contract.Truncation, 0)
		if len(events) > limit {
			sourceCount := len(events)
			events = events[:limit]
			truncation = append(truncation, contract.Truncation{
				Path: "data.events", Reason: "item_limit", SourceCount: &sourceCount, EmittedCount: limit,
			})
		}
		for index, eventValue := range events {
			event, ok := eventValue.(map[string]any)
			if !ok {
				continue
			}
			markets, ok := event["markets"].([]any)
			if !ok || len(markets) <= limit {
				continue
			}
			sourceCount := len(markets)
			event["markets"] = markets[:limit]
			truncation = append(truncation, contract.Truncation{
				Path: fmt.Sprintf("data.events[%d].markets", index), Reason: "nested_item_limit",
				SourceCount: &sourceCount, EmittedCount: limit,
			})
		}
		result["events"] = events
		return result, truncation
	}
	book, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	for _, side := range []string{"bids", "asks"} {
		levels, ok := book[side].([]any)
		if !ok || len(levels) <= limit {
			continue
		}
		sourceCount := len(levels)
		book[side] = levels[:limit]
		truncation = append(truncation, contract.Truncation{
			Path: "data." + side, Reason: "order_book_depth_limit", SourceCount: &sourceCount, EmittedCount: limit,
		})
	}
	return book, truncation
}

func collectionComplete(invocation invocation, value any) bool {
	if invocation.Command.ID == "markets.search" {
		if document, ok := value.(map[string]any); ok {
			if pagination, ok := document["pagination"].(map[string]any); ok {
				if hasMore, ok := pagination["hasMore"].(bool); ok {
					return !hasMore
				}
			}
		}
		return false
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	switch request := invocation.Request.(type) {
	case *marketsListRequest:
		return len(items) < request.Limit
	case *eventsListRequest:
		return len(items) < request.Limit
	default:
		return false
	}
}

func readMeta(providerName string) contract.Meta {
	return contract.Meta{Provider: providerName, Effects: contract.Effects{
		Executed: true, Network: contract.NetworkRead, Mutation: contract.MutationNone,
		Risk: contract.RiskNone,
	}}
}

func decodeRaw(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, contract.NewError("invalid_provider_response", contract.CategoryProvider, "Polymarket returned an invalid JSON response", contract.ExitRejected).WithCause(err)
	}
	return value, nil
}

func collectionCount(value any) int {
	switch typed := value.(type) {
	case []any:
		return len(typed)
	case map[string]any:
		if events, ok := typed["events"].([]any); ok {
			return len(events)
		}
	}
	return 0
}

func mapExecutionError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *contract.Error
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.Canceled) {
		return contract.NewError("interrupted", contract.CategoryTransient, "operation was interrupted", contract.ExitInterrupted)
	}
	var validation *provider.ValidationError
	if errors.As(err, &validation) {
		return contract.Invalid("invalid_parameter", validation.Error())
	}
	var transport *httpx.Error
	if errors.As(err, &transport) {
		details := map[string]any{"service": transport.Service}
		if transport.StatusCode != 0 {
			details["status"] = transport.StatusCode
		}
		switch transport.Kind {
		case httpx.ErrorUnauthorized:
			return contract.NewError("authentication_failed", contract.CategoryAuth, "Polymarket rejected authentication", contract.ExitAuth).WithDetails(details)
		case httpx.ErrorNotFound:
			return contract.NewError("not_found", contract.CategoryNotFound, "Polymarket resource was not found", contract.ExitNotFound).WithDetails(details)
		case httpx.ErrorRateLimited:
			result := contract.NewError("rate_limited", contract.CategoryTransient, "Polymarket rate limited the request", contract.ExitTransient).WithDetails(details)
			result.Retryable = true
			return result
		case httpx.ErrorTimeout:
			result := contract.NewError("timeout", contract.CategoryTransient, "Polymarket request timed out", contract.ExitTransient).WithDetails(details)
			result.Retryable = true
			return result
		case httpx.ErrorTransport, httpx.ErrorServer:
			result := contract.NewError("provider_unavailable", contract.CategoryTransient, "Polymarket is temporarily unavailable", contract.ExitTransient).WithDetails(details)
			result.Retryable = true
			return result
		case httpx.ErrorResponseTooLarge:
			return contract.NewError("provider_response_too_large", contract.CategoryProvider, "Polymarket response exceeded the safety limit", contract.ExitRejected).WithDetails(details)
		default:
			return contract.NewError("provider_rejected", contract.CategoryProvider, "Polymarket rejected the request", contract.ExitRejected).WithDetails(details)
		}
	}
	return contract.Internal("unexpected execution failure", err)
}

func (a *App) fail(writer *output.Writer, command string, err error) int {
	exit, writeErr := writer.Failure(command, err)
	if writeErr != nil {
		return contract.ExitInternal.Int()
	}
	return exit.Int()
}

func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func commandHint(arguments []string) string {
	if len(arguments) == 0 {
		return ""
	}
	if len(arguments) == 1 {
		return arguments[0]
	}
	return arguments[0] + "." + arguments[1]
}

func helpText() string {
	return strings.TrimSpace(`pmx is an agent-first Polymarket CLI.

Usage:
  pmx schema [group|group.command]
  pmx doctor
  pmx markets list [--active=true] [--closed=false] [--limit 25]
  pmx markets get <id-or-slug>
  pmx markets search <query> [--limit 10]
  pmx events list [--tag politics] [--limit 25]
  pmx events get <id-or-slug>
  pmx clob price <token-id> --side BUY
  pmx clob midpoint|spread|book|tick-size|fee-rate|neg-risk|last-trade <token-id>
  pmx clob time

Agent controls:
  --params JSON|-   Strict schema-checked request object
  --fields PATHS    Project data fields without hiding envelope metadata
  --compact         Emit one-line JSON
  --timeout DURATION  Request timeout from 1ms to 1m

Run "pmx schema" for the machine-readable command index.
`) + "\n"
}
