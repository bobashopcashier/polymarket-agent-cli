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
	"path/filepath"
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/console"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/httpx"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/output"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/params"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/provider"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/sanitize"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/transaction"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/upstream"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/wallet"
)

type Dependencies struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	HTTPClient *http.Client
	Console    console.Console
	Wallets    *wallet.Manager
	Upstream   *upstream.Runner
	TxSender   *transaction.Sender
}

type App struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	httpClient *http.Client
	commands   *registry.Registry
	console    console.Console
	wallets    *wallet.Manager
	upstream   *upstream.Runner
	txSender   *transaction.Sender
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
	if dependencies.Console == nil {
		dependencies.Console = console.TTY{}
	}
	if dependencies.Wallets == nil {
		manager, managerErr := defaultWalletManager()
		if managerErr != nil {
			return nil, managerErr
		}
		dependencies.Wallets = manager
	}
	if dependencies.Upstream == nil {
		dependencies.Upstream = discoverUpstream()
	}
	if dependencies.TxSender == nil {
		dependencies.TxSender = &transaction.Sender{Client: dependencies.HTTPClient}
	}
	return &App{
		stdin: dependencies.Stdin, stdout: dependencies.Stdout, stderr: dependencies.Stderr,
		httpClient: dependencies.HTTPClient, commands: commands, console: dependencies.Console,
		wallets: dependencies.Wallets, upstream: dependencies.Upstream, txSender: dependencies.TxSender,
	}, nil
}

func defaultWalletManager() (*wallet.Manager, error) {
	configurationDirectory, err := os.UserConfigDir()
	if err != nil || configurationDirectory == "" {
		return nil, contract.Internal("could not locate the user configuration directory", err)
	}
	store, err := wallet.NewKeyringStore("pmx.polymarket")
	if err != nil {
		return nil, contract.Internal("could not configure the operating-system keychain", err)
	}
	manager, err := wallet.NewManager(filepath.Join(configurationDirectory, "pmx", "wallets.json"), store)
	if err != nil {
		return nil, contract.Internal("could not configure wallet metadata", err)
	}
	return manager, nil
}

func discoverUpstream() *upstream.Runner {
	path := strings.TrimSpace(os.Getenv("PMX_POLYMARKET_BIN"))
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return nil
	}
	runner, err := upstream.New(upstream.Options{ExecutablePath: resolved})
	if err != nil {
		return nil
	}
	return runner
}

func (a *App) Run(ctx context.Context, arguments []string) int {
	if ctx == nil {
		ctx = context.Background()
	}
	options, remaining, parseErr := parseGlobal(arguments)
	writer := output.NewWriter(a.stdout, a.stderr, output.Options{
		Format: output.FormatJSON, Compact: options.Compact, Fields: options.Fields,
		MaximumBytes: output.DefaultMaximumBytes,
	})
	if parseErr != nil {
		return a.fail(writer, "", parseErr)
	}
	if err := params.RejectArgumentControls(arguments); err != nil {
		return a.fail(writer, commandHint(remaining), err)
	}
	executionContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	if options.Execute && (options.Version || options.Help || len(remaining) == 0 || remaining[0] == "version" || remaining[0] == "help" || remaining[0] == "schema") {
		return a.fail(writer, commandHint(remaining), contract.Invalid("execute_not_supported", "--execute is accepted only by mutation commands"))
	}
	if options.DryRun && (options.Version || options.Help || len(remaining) == 0 || remaining[0] == "version" || remaining[0] == "help" || remaining[0] == "schema") {
		return a.fail(writer, commandHint(remaining), contract.Invalid("dry_run_not_supported", "--dry-run is accepted only by mutation commands"))
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

	invocation, err := parseInvocation(executionContext, arguments, a.stdin, a.commands)
	if err != nil {
		return a.fail(writer, commandHint(remaining), mapExecutionError(err))
	}
	if invocation.Options.Fields != "" {
		if err := output.ValidateFieldMask(invocation.Options.Fields, invocation.Command.Output.ResponseFields); err != nil {
			return a.fail(writer, invocation.Command.ID, err)
		}
	}
	if invocation.Options.Execute && !invocation.Command.Effects.Effects.IsMutation() {
		return a.fail(writer, invocation.Command.ID, contract.Invalid("execute_not_supported", "--execute is accepted only by mutation commands"))
	}
	if invocation.Options.DryRun && !invocation.Command.Effects.Effects.IsMutation() {
		return a.fail(writer, invocation.Command.ID, contract.Invalid("dry_run_not_supported", "--dry-run is accepted only by mutation commands"))
	}
	writer.Options.MaximumBytes = invocation.Command.Output.MaximumEncodedOutputBytes
	data, meta, err := a.execute(executionContext, invocation)
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
	if invocation.Command.Effects.Effects.IsMutation() {
		return a.executeMutation(ctx, invocation)
	}
	if isManagedRead(invocation.Command.ID) {
		return a.executeManagedRead(ctx, invocation)
	}
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
		wallets, err := a.wallets.List(ctx)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		configured := false
		secretReady := false
		if len(wallets) != 0 {
			if active, activeErr := a.wallets.Active(ctx); activeErr == nil {
				configured = active.SignatureType == wallet.SignatureEOA
				if configured {
					_, checkErr := a.wallets.Check(ctx, active.Name)
					secretReady = checkErr == nil
				}
			} else if !errors.Is(activeErr, wallet.ErrNoActiveWallet) {
				return nil, contract.Meta{}, activeErr
			}
		}
		data := map[string]any{
			"ready":          true,
			"gamma":          map[string]any{"reachable": true, "sampleItems": collectionCount(gammaData)},
			"clob":           map[string]any{"reachable": true, "status": clobData},
			"authentication": map[string]any{"required": false, "configured": configured, "secretReady": secretReady, "tradingEnabled": configured && secretReady && a.upstream != nil},
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
	if command.ID == "orders.list" || command.ID == "trades.list" {
		result, ok := value.(map[string]any)
		if !ok {
			return value, nil
		}
		items, ok := result["data"].([]any)
		if !ok || len(items) <= limit {
			return value, nil
		}
		sourceCount := len(items)
		result["data"] = items[:limit]
		return result, []contract.Truncation{{Path: "data.data", Reason: "item_limit", SourceCount: &sourceCount, EmittedCount: limit}}
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
		if data, ok := typed["data"].([]any); ok {
			return len(data)
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
	if errors.Is(err, context.DeadlineExceeded) {
		result := contract.NewError("timeout", contract.CategoryTransient, "operation timed out", contract.ExitTransient)
		result.Retryable = true
		return result
	}
	var validation *provider.ValidationError
	if errors.As(err, &validation) {
		return contract.Invalid("invalid_parameter", validation.Error())
	}
	if errors.Is(err, wallet.ErrWalletNotFound) || errors.Is(err, wallet.ErrNoActiveWallet) || errors.Is(err, wallet.ErrSecretNotFound) {
		return contract.NewError("wallet_not_configured", contract.CategoryAuth, "the requested wallet profile or its secret is not configured", contract.ExitAuth)
	}
	if errors.Is(err, wallet.ErrWalletExists) {
		return contract.Invalid("wallet_exists", "a wallet profile with that name already exists")
	}
	if errors.Is(err, wallet.ErrAddressMismatch) {
		return contract.NewError("wallet_address_mismatch", contract.CategoryAuth, "the private key does not match the expected wallet address", contract.ExitAuth)
	}
	if errors.Is(err, wallet.ErrWalletChanged) {
		return contract.PolicyDenied("wallet_changed", "wallet metadata changed after the operation was authorized")
	}
	if errors.Is(err, wallet.ErrInvalidSecret) {
		return contract.NewError("invalid_private_key", contract.CategoryAuth, "the masked input is not a valid secp256k1 private key", contract.ExitAuth)
	}
	if errors.Is(err, wallet.ErrKeyringUnavailable) || errors.Is(err, wallet.ErrSecretStore) {
		return contract.NewError("keychain_unavailable", contract.CategoryAuth, "the operating-system keychain is unavailable", contract.ExitAuth)
	}
	if errors.Is(err, wallet.ErrUnsafeMetadata) || errors.Is(err, wallet.ErrInvalidMetadata) {
		return contract.PolicyDenied("unsafe_wallet_metadata", "wallet metadata failed its integrity or permission checks")
	}
	var upstreamError *upstream.Error
	if errors.As(err, &upstreamError) {
		switch upstreamError.Kind {
		case upstream.ErrorInvalidConfig, upstream.ErrorStartFailed:
			return upstreamUnavailable()
		case upstream.ErrorInvalidCommand:
			return contract.Invalid("invalid_upstream_request", upstreamError.Message)
		case upstream.ErrorMissingSecret:
			return contract.NewError("wallet_secret_unavailable", contract.CategoryAuth, "the wallet secret is unavailable", contract.ExitAuth)
		case upstream.ErrorCanceled:
			return contract.NewError("interrupted", contract.CategoryTransient, "the official Polymarket CLI was interrupted", contract.ExitInterrupted)
		case upstream.ErrorTimeout:
			result := contract.NewError("upstream_timeout", contract.CategoryTransient, "the official Polymarket CLI timed out", contract.ExitTransient)
			result.Retryable = true
			return result
		case upstream.ErrorOutputTooLarge:
			return contract.NewError("upstream_output_too_large", contract.CategoryProvider, "the official Polymarket CLI exceeded its output safety limit", contract.ExitRejected)
		case upstream.ErrorInvalidJSON:
			return contract.NewError("invalid_upstream_response", contract.CategoryProvider, "the official Polymarket CLI returned invalid JSON", contract.ExitRejected)
		default:
			return contract.NewError("upstream_rejected", contract.CategoryProvider, "the official Polymarket CLI rejected the request", contract.ExitRejected)
		}
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
  pmx auth status
  pmx wallet create|import|list|show|use|remove|sign-message
  pmx orders list|get|create|cancel|cancel-batch|cancel-market|cancel-all
  pmx trades list
  pmx balances get
  pmx approvals check|set
  pmx transactions inspect|submit

Agent controls:
  --params JSON|-   Strict schema-checked request object
  --output json     Emit the versioned machine-readable JSON envelope (default)
  --json            Legacy alias for --output json
  --fields PATHS    Project data fields without hiding envelope metadata
  --compact         Emit one-line JSON
  --dry-run         Explicitly plan a mutation without executing it (default)
  --execute         Request a mutation; still requires controlling-TTY approval
  --timeout DURATION  Request timeout from 1ms to 1m

Run "pmx schema" for the machine-readable command index.
`) + "\n"
}
