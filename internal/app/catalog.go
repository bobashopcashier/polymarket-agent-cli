package app

import (
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
)

const version = "0.2.0"

type marketsListRequest struct {
	Active    *bool  `json:"active,omitempty"`
	Closed    *bool  `json:"closed,omitempty"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset,omitempty"`
	Order     string `json:"order,omitempty"`
	Ascending *bool  `json:"ascending,omitempty"`
}

type resourceRequest struct {
	ID string `json:"id"`
}

type searchRequest struct {
	Query string `json:"q"`
	Limit int    `json:"limit"`
}

type eventsListRequest struct {
	Active    *bool  `json:"active,omitempty"`
	Closed    *bool  `json:"closed,omitempty"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset,omitempty"`
	Order     string `json:"order,omitempty"`
	Ascending *bool  `json:"ascending,omitempty"`
	Tag       string `json:"tag,omitempty"`
}

type tokenRequest struct {
	TokenID string `json:"tokenId"`
}

type priceRequest struct {
	TokenID string `json:"tokenId"`
	Side    string `json:"side"`
}

func commandSpecs() []registry.CommandSpec {
	specs := []registry.CommandSpec{
		readCommand("doctor", []string{"doctor"}, "Check local readiness and public API reachability", nil, objectResponse(), false, 1<<20),
		readCommand("markets.list", []string{"markets", "list"}, "List Gamma markets with bounded pagination", marketListFields(), arrayResponse(), true, 16<<20),
		readCommand("markets.get", []string{"markets", "get"}, "Get one Gamma market by numeric ID or slug", []registry.FieldSpec{resourceField()}, objectResponse(), false, 16<<20),
		readCommand("markets.search", []string{"markets", "search"}, "Search public Polymarket markets and events", []registry.FieldSpec{
			stringField("q", true, 256, "Search query"),
			integerField("limit", false, 1, 100, 10, "Maximum results per entity type"),
		}, objectResponse(), true, 16<<20),
		readCommand("events.list", []string{"events", "list"}, "List Gamma events with bounded pagination", eventListFields(), arrayResponse(), true, 16<<20),
		readCommand("events.get", []string{"events", "get"}, "Get one Gamma event by numeric ID or slug", []registry.FieldSpec{resourceField()}, objectResponse(), false, 16<<20),
		readCommand("clob.price", []string{"clob", "price"}, "Get the best executable price for a token and side", []registry.FieldSpec{
			tokenField(), enumField("side", true, []string{"BUY", "SELL"}, "Order side"),
		}, objectResponse(), false, 8<<20),
		readCommand("clob.midpoint", []string{"clob", "midpoint"}, "Get the midpoint price for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 8<<20),
		readCommand("clob.spread", []string{"clob", "spread"}, "Get the bid-ask spread for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 8<<20),
		readCommand("clob.book", []string{"clob", "book"}, "Get a bounded order book snapshot for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 8<<20),
		readCommand("clob.tick-size", []string{"clob", "tick-size"}, "Get the minimum price tick for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 1<<20),
		readCommand("clob.fee-rate", []string{"clob", "fee-rate"}, "Get the base fee rate for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 1<<20),
		readCommand("clob.neg-risk", []string{"clob", "neg-risk"}, "Get the negative-risk setting for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 1<<20),
		readCommand("clob.last-trade", []string{"clob", "last-trade"}, "Get the most recent trade price for a token", []registry.FieldSpec{tokenField()}, objectResponse(), false, 1<<20),
		readCommand("clob.time", []string{"clob", "time"}, "Get the CLOB server time", nil, registry.ValueSpec{Kind: registry.KindInteger}, false, 1<<20),
	}
	return append(specs, authenticatedCommandSpecs()...)
}

func newCommandRegistry() (*registry.Registry, error) {
	specs := commandSpecs()
	for index := range specs {
		id := specs[index].ID
		specs[index].NewRequest = func() any { return newRequest(id) }
		specs[index].Output.ResponseFields = responseFields(id)
		if len(specs[index].Examples) == 0 {
			specs[index].Examples = commandExamples(id)
		}
		if id == "clob.book" {
			specs[index].Output.HardItemLimit = 200
		}
	}
	return registry.New(version, specs...)
}

func commandExamples(id string) []registry.Example {
	const tokenID = "1234567890"
	examples := map[string]registry.Example{
		"doctor":          {Summary: "Check public endpoint readiness", Command: "pmx doctor --compact", Params: map[string]any{}},
		"markets.list":    {Summary: "List active markets", Command: `pmx markets list --params '{"active":true,"closed":false,"limit":10}'`, Params: map[string]any{"active": true, "closed": false, "limit": 10}},
		"markets.get":     {Summary: "Get a market by slug", Command: `pmx markets get --params '{"id":"example-market-slug"}'`, Params: map[string]any{"id": "example-market-slug"}},
		"markets.search":  {Summary: "Search markets and events", Command: `pmx markets search --params '{"q":"bitcoin","limit":5}'`, Params: map[string]any{"q": "bitcoin", "limit": 5}},
		"events.list":     {Summary: "List active events", Command: `pmx events list --params '{"active":true,"closed":false,"limit":10}'`, Params: map[string]any{"active": true, "closed": false, "limit": 10}},
		"events.get":      {Summary: "Get an event by slug", Command: `pmx events get --params '{"id":"example-event-slug"}'`, Params: map[string]any{"id": "example-event-slug"}},
		"clob.price":      {Summary: "Get the best buy price", Command: `pmx clob price --params '{"tokenId":"1234567890","side":"BUY"}'`, Params: map[string]any{"tokenId": tokenID, "side": "BUY"}},
		"clob.midpoint":   tokenExample("midpoint", tokenID),
		"clob.spread":     tokenExample("spread", tokenID),
		"clob.book":       tokenExample("book", tokenID),
		"clob.tick-size":  tokenExample("tick-size", tokenID),
		"clob.fee-rate":   tokenExample("fee-rate", tokenID),
		"clob.neg-risk":   tokenExample("neg-risk", tokenID),
		"clob.last-trade": tokenExample("last-trade", tokenID),
		"clob.time":       {Summary: "Get CLOB server time", Command: `pmx clob time --params '{}'`, Params: map[string]any{}},
	}
	example, ok := examples[id]
	if !ok {
		return nil
	}
	return []registry.Example{example}
}

func tokenExample(command, tokenID string) registry.Example {
	return registry.Example{
		Summary: "Read public token data",
		Command: `pmx clob ` + command + ` --params '{"tokenId":"` + tokenID + `"}'`,
		Params:  map[string]any{"tokenId": tokenID},
	}
}

func responseFields(id string) []string {
	switch id {
	case "doctor":
		return []string{"ready", "gamma.reachable", "gamma.sampleItems", "clob.reachable", "clob.status", "authentication.required", "authentication.configured", "authentication.secretReady", "authentication.tradingEnabled"}
	case "markets.list", "markets.get":
		return []string{"id", "question", "slug", "active", "closed", "description", "clobTokenIds", "outcomes", "outcomePrices", "bestBid", "bestAsk", "lastTradePrice", "spread", "volume", "liquidity", "conditionId", "events"}
	case "markets.search":
		return []string{"events", "events.id", "events.title", "events.slug", "events.markets", "pagination"}
	case "events.list", "events.get":
		return []string{"id", "title", "slug", "active", "closed", "description", "markets", "tags", "volume", "liquidity", "startDate", "endDate"}
	case "clob.price":
		return []string{"price"}
	case "clob.last-trade":
		return []string{"price", "side"}
	case "clob.midpoint":
		return []string{"mid"}
	case "clob.spread":
		return []string{"spread"}
	case "clob.tick-size":
		return []string{"minimum_tick_size"}
	case "clob.fee-rate":
		return []string{"base_fee"}
	case "clob.neg-risk":
		return []string{"neg_risk"}
	case "clob.book":
		return []string{"market", "asset_id", "timestamp", "hash", "bids", "bids.price", "bids.size", "asks", "asks.price", "asks.size", "min_order_size", "tick_size", "neg_risk", "last_trade_price"}
	case "auth.status":
		return []string{"walletConfigured", "walletSecretReady", "walletError", "wallet", "address", "signatureType", "upstreamConfigured", "upstreamPath", "upstreamSha256", "upstreamRequiredRevision", "upstreamRevisionAttested", "ready", "protocol", "chainId"}
	case "wallet.list":
		return []string{"active", "wallets", "wallets.name", "wallets.address", "wallets.signatureType", "wallets.funder"}
	case "wallet.show":
		return []string{"name", "address", "signatureType", "funder", "active", "stored"}
	case "wallet.create", "wallet.import", "wallet.use", "wallet.remove":
		return mutationResponseFields("name", "address", "signatureType", "funder", "active", "stored", "removed")
	case "wallet.sign-message":
		return mutationResponseFields("address", "messageHash", "signature", "mode")
	case "transactions.inspect":
		return []string{"hash", "chainId", "from", "to", "nonce", "valueWei", "gas", "gasPriceWei", "maxFeePerGasWei", "maxPriorityFeeWei", "maxExecutionFeeWei", "type", "dataBytes", "selector", "dataPreview", "dataHash", "accessListEntries", "blobCount", "blobGasFeeCapWei", "rawTransactionHash"}
	case "transactions.submit":
		return mutationResponseFields("submitted", "hash", "transaction")
	case "orders.list":
		return []string{"data", "data.id", "data.status", "data.owner", "data.maker_address", "data.market", "data.asset_id", "data.side", "data.price", "data.original_size", "data.size_matched", "data.associate_trades", "data.outcome", "data.order_type", "data.created_at", "data.expiration", "next_cursor"}
	case "trades.list":
		return []string{"data", "data.id", "data.taker_order_id", "data.market", "data.asset_id", "data.side", "data.size", "data.price", "data.fee_rate_bps", "data.status", "data.match_time", "data.last_update", "data.outcome", "data.bucket_index", "data.owner", "data.maker_address", "data.maker_orders", "data.trader_side", "data.transaction_hash", "data.error_msg", "next_cursor"}
	case "orders.get", "orders.create", "orders.cancel", "orders.cancel-batch", "orders.cancel-market", "orders.cancel-all", "balances.get", "approvals.check", "approvals.set":
		if id == "orders.create" || id == "orders.cancel" || id == "orders.cancel-batch" || id == "orders.cancel-market" || id == "orders.cancel-all" || id == "approvals.set" {
			return mutationResponseFields()
		}
		return nil
	default:
		return nil
	}
}

func mutationResponseFields(resultFields ...string) []string {
	fields := []string{"dryRun", "executes", "plan", "result"}
	for _, field := range resultFields {
		fields = append(fields, "result."+field)
	}
	return fields
}

func newRequest(id string) any {
	switch id {
	case "markets.list":
		return &marketsListRequest{}
	case "markets.get", "events.get":
		return &resourceRequest{}
	case "markets.search":
		return &searchRequest{}
	case "events.list":
		return &eventsListRequest{}
	case "clob.price":
		return &priceRequest{}
	case "clob.midpoint", "clob.spread", "clob.book", "clob.tick-size", "clob.fee-rate", "clob.neg-risk", "clob.last-trade":
		return &tokenRequest{}
	case "doctor", "clob.time":
		return &struct{}{}
	case "auth.status", "wallet.list":
		return &struct{}{}
	case "wallet.create":
		return &walletCreateRequest{}
	case "wallet.import":
		return &walletImportRequest{}
	case "wallet.use", "wallet.remove":
		return &walletNameRequest{}
	case "wallet.show", "approvals.check":
		return &walletSelectionRequest{}
	case "wallet.sign-message":
		return &signMessageRequest{}
	case "orders.list", "trades.list":
		return &authenticatedListRequest{}
	case "orders.get":
		return &authenticatedIDRequest{}
	case "balances.get":
		return &balanceRequest{}
	case "orders.create":
		return &limitOrderRequest{}
	case "orders.cancel":
		return &cancelOrderRequest{}
	case "orders.cancel-batch":
		return &cancelBatchRequest{}
	case "orders.cancel-market":
		return &cancelMarketRequest{}
	case "orders.cancel-all":
		return &cancelAllRequest{}
	case "approvals.set":
		return &approvalRequest{}
	case "transactions.inspect":
		return &rawTransactionRequest{}
	case "transactions.submit":
		return &rawTransactionSubmitRequest{}
	default:
		return nil
	}
}

func readCommand(id string, path []string, summary string, fields []registry.FieldSpec, response registry.ValueSpec, collection bool, providerBytes int64) registry.CommandSpec {
	return registry.CommandSpec{
		ID: id, Path: path, Summary: summary, AgentInvocable: true,
		Params: registry.ObjectSpec{
			MaximumBytes: 64 << 10, AdditionalProperties: false,
			OutputControls: []string{"--json", "--compact", "--fields"}, Fields: fields,
		},
		Response: response,
		Auth:     registry.AuthSpec{Mode: registry.AuthNone},
		Effects: registry.EffectSpec{Effects: contract.Effects{
			Network: contract.NetworkRead, Mutation: contract.MutationNone, Risk: contract.RiskNone,
		}, Confirmation: registry.ConfirmationNone, Idempotent: true},
		Output: registry.OutputSpec{
			Collection: collection, Formats: []string{"json"}, MaximumProviderBytes: providerBytes,
			MaximumEncodedOutputBytes: 8 << 20, DefaultItemLimit: defaultIf(collection, 25), HardItemLimit: defaultIf(collection, 100),
		},
	}
}

func defaultIf(condition bool, value int) int {
	if condition {
		return value
	}
	return 0
}

func objectResponse() registry.ValueSpec {
	return registry.ValueSpec{Kind: registry.KindObject, AdditionalProperties: true}
}

func arrayResponse() registry.ValueSpec {
	return registry.ValueSpec{Kind: registry.KindArray, Items: &registry.ValueSpec{Kind: registry.KindObject, AdditionalProperties: true}}
}

func marketListFields() []registry.FieldSpec {
	return []registry.FieldSpec{
		booleanField("active", false, "Filter by active state"),
		booleanField("closed", false, "Filter by closed state"),
		integerField("limit", false, 1, 100, 25, "Maximum markets"),
		integerField("offset", false, 0, 100000, 0, "Pagination offset"),
		stringField("order", false, 64, "Gamma sort field"),
		booleanField("ascending", false, "Sort ascending"),
	}
}

func eventListFields() []registry.FieldSpec {
	fields := marketListFields()
	fields = append(fields, stringField("tag", false, 128, "Tag slug filter"))
	return fields
}

func resourceField() registry.FieldSpec {
	return stringField("id", true, 256, "Numeric resource ID or slug")
}

func tokenField() registry.FieldSpec {
	return registry.FieldSpec{Name: "tokenId", Flag: "token-id", Kind: registry.KindString, Required: true, Pattern: `^[0-9]{1,78}$`, Format: "uint256-decimal", MaxBytes: 78, Description: "Numeric CLOB token ID"}
}

func stringField(name string, required bool, maxBytes int, description string) registry.FieldSpec {
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindString, Required: required, MaxBytes: maxBytes, Normalize: registry.NormalizeTrim, Description: description}
}

func booleanField(name string, required bool, description string) registry.FieldSpec {
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindBoolean, Required: required, Description: description}
}

func integerField(name string, required bool, min, max, defaultValue int, description string) registry.FieldSpec {
	minimum, maximum := intString(min), intString(max)
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindInteger, Required: required, Minimum: &minimum, Maximum: &maximum, Default: defaultValue, Description: description}
}

func enumField(name string, required bool, values []string, description string) registry.FieldSpec {
	normalize := registry.NormalizeUppercase
	if len(values) > 0 && values[0] == strings.ToLower(values[0]) {
		normalize = registry.NormalizeLowercase
	}
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindString, Required: required, Enum: values, Normalize: normalize, Description: description}
}

func flagName(name string) string {
	result := make([]byte, 0, len(name)+4)
	for _, current := range []byte(name) {
		if current >= 'A' && current <= 'Z' {
			result = append(result, '-', current+'a'-'A')
			continue
		}
		result = append(result, current)
	}
	return string(result)
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
