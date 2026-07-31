package app

import (
	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
)

type walletCreateRequest struct {
	Name          string `json:"name"`
	SignatureType string `json:"signatureType"`
}

type walletImportRequest struct {
	Name            string `json:"name"`
	ExpectedAddress string `json:"expectedAddress"`
	SignatureType   string `json:"signatureType"`
}

type walletNameRequest struct {
	Name string `json:"name,omitempty"`
}

type walletSelectionRequest struct {
	Wallet string `json:"wallet,omitempty"`
}

type signMessageRequest struct {
	Wallet          string `json:"wallet,omitempty"`
	Message         string `json:"message"`
	ClientRequestID string `json:"clientRequestId"`
}

type authenticatedListRequest struct {
	Wallet string `json:"wallet,omitempty"`
	Market string `json:"market,omitempty"`
	Token  string `json:"tokenId,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type authenticatedIDRequest struct {
	Wallet string `json:"wallet,omitempty"`
	ID     string `json:"id"`
}

type balanceRequest struct {
	Wallet    string `json:"wallet,omitempty"`
	AssetType string `json:"assetType"`
	TokenID   string `json:"tokenId,omitempty"`
}

type limitOrderRequest struct {
	Wallet          string `json:"wallet,omitempty"`
	TokenID         string `json:"tokenId"`
	Side            string `json:"side"`
	Price           string `json:"price"`
	Size            string `json:"size"`
	MaxNotionalPUSD string `json:"maxNotionalPusd"`
	OrderType       string `json:"orderType"`
	ClientRequestID string `json:"clientRequestId"`
}

type cancelOrderRequest struct {
	Wallet          string `json:"wallet,omitempty"`
	OrderID         string `json:"orderId"`
	ClientRequestID string `json:"clientRequestId"`
}

type cancelBatchRequest struct {
	Wallet          string   `json:"wallet,omitempty"`
	OrderIDs        []string `json:"orderIds"`
	ClientRequestID string   `json:"clientRequestId"`
}

type cancelMarketRequest struct {
	Wallet          string `json:"wallet,omitempty"`
	Market          string `json:"market,omitempty"`
	TokenID         string `json:"tokenId,omitempty"`
	ClientRequestID string `json:"clientRequestId"`
}

type cancelAllRequest struct {
	Wallet          string `json:"wallet,omitempty"`
	Scope           string `json:"scope"`
	ClientRequestID string `json:"clientRequestId"`
}

type approvalRequest struct {
	Wallet          string `json:"wallet,omitempty"`
	Scope           string `json:"scope"`
	ClientRequestID string `json:"clientRequestId"`
}

type rawTransactionRequest struct {
	RawTransactionFile string `json:"rawTransactionFile"`
	ClientRequestID    string `json:"clientRequestId,omitempty"`
}

type rawTransactionSubmitRequest struct {
	RawTransactionFile string `json:"rawTransactionFile"`
	Scope              string `json:"scope"`
	ClientRequestID    string `json:"clientRequestId"`
}

func authenticatedCommandSpecs() []registry.CommandSpec {
	read := []registry.CommandSpec{
		localReadCommand("auth.status", []string{"auth", "status"}, "Check signer and official execution-engine readiness", nil, false, example("Inspect authenticated readiness", "pmx auth status", map[string]any{})),
		authReadCommand("orders.list", []string{"orders", "list"}, "List authenticated open orders", authenticatedListFields(), example("List open orders", `pmx orders list --params '{}'`, map[string]any{})),
		authReadCommand("orders.get", []string{"orders", "get"}, "Get one authenticated order", []registry.FieldSpec{walletField(), orderIDField("id", true)}, example("Get one order", `pmx orders get --params '{"id":"0x0000000000000000000000000000000000000000000000000000000000000000"}'`, map[string]any{"id": zeroHash()})),
		authReadCommand("trades.list", []string{"trades", "list"}, "List authenticated trades", authenticatedListFields(), example("List authenticated trades", `pmx trades list --params '{}'`, map[string]any{})),
		authReadCommand("balances.get", []string{"balances", "get"}, "Get authenticated collateral or conditional-token balance", []registry.FieldSpec{
			walletField(), enumField("assetType", true, []string{"collateral", "conditional"}, "Balance asset type"), tokenFieldOptional(),
		}, example("Read pUSD balance", `pmx balances get --params '{"assetType":"collateral"}'`, map[string]any{"assetType": "collateral"})),
		approvalCheckCommand(),
	}

	mutations := []registry.CommandSpec{
		mutationCommand("orders.create", []string{"orders", "create"}, "Create a signed limit order through the official CLOB V2 client", true, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationOrderCreate, Signing: true, Financial: true, Broadcast: true, Reversible: false, Risk: contract.RiskHigh}, []registry.FieldSpec{
			walletField(), tokenField(), enumField("side", true, []string{"BUY", "SELL"}, "Order side"), priceField(), positiveDecimalField("size", "Share quantity"), positiveDecimalField("maxNotionalPusd", "Caller-authorized maximum price times size in pUSD"), enumFieldDefault("orderType", []string{"GTC"}, "GTC", "Order time in force"), clientRequestIDField(),
		}, example("Plan a post-only limit order", `pmx orders create --params '{"tokenId":"1234567890","side":"BUY","price":"0.45","size":"10","maxNotionalPusd":"5","clientRequestId":"order-20260731-001"}'`, map[string]any{"tokenId": "1234567890", "side": "BUY", "price": "0.45", "size": "10", "maxNotionalPusd": "5", "clientRequestId": "order-20260731-001"})),
		mutationCommand("orders.cancel", []string{"orders", "cancel"}, "Cancel one open order", true, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationOrderCancel, Signing: true, Financial: true, Broadcast: true, Reversible: false, Risk: contract.RiskHigh}, []registry.FieldSpec{walletField(), orderIDField("orderId", true), clientRequestIDField()}, example("Plan one cancellation", `pmx orders cancel --params '{"orderId":"0x0000000000000000000000000000000000000000000000000000000000000000","clientRequestId":"cancel-20260731-001"}'`, map[string]any{"orderId": zeroHash(), "clientRequestId": "cancel-20260731-001"})),
		mutationCommand("orders.cancel-batch", []string{"orders", "cancel-batch"}, "Cancel a bounded batch of open orders", true, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationOrderCancel, Signing: true, Financial: true, Broadcast: true, Reversible: false, Risk: contract.RiskHigh}, []registry.FieldSpec{walletField(), orderIDsField(), clientRequestIDField()}, example("Plan a batch cancellation", `pmx orders cancel-batch --params '{"orderIds":["0x0000000000000000000000000000000000000000000000000000000000000000"],"clientRequestId":"cancel-batch-001"}'`, map[string]any{"orderIds": []string{zeroHash()}, "clientRequestId": "cancel-batch-001"})),
		mutationCommand("orders.cancel-market", []string{"orders", "cancel-market"}, "Cancel orders for an explicit market or token", true, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationOrderCancel, Signing: true, Financial: true, Broadcast: true, Reversible: false, Risk: contract.RiskHigh}, []registry.FieldSpec{walletField(), conditionField("market", false), tokenFieldOptional(), clientRequestIDField()}, example("Plan a market cancellation", `pmx orders cancel-market --params '{"market":"0x0000000000000000000000000000000000000000000000000000000000000000","clientRequestId":"cancel-market-001"}'`, map[string]any{"market": zeroHash(), "clientRequestId": "cancel-market-001"})),
		mutationCommand("orders.cancel-all", []string{"orders", "cancel-all"}, "Cancel every open order for the active wallet", false, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationCancelAll, Signing: true, Financial: true, Broadcast: true, Reversible: false, Risk: contract.RiskCritical}, []registry.FieldSpec{walletField(), enumField("scope", true, []string{"ALL"}, "Required explicit all-orders scope"), clientRequestIDField()}, example("Plan cancel-all", `pmx orders cancel-all --params '{"scope":"ALL","clientRequestId":"cancel-all-001"}'`, map[string]any{"scope": "ALL", "clientRequestId": "cancel-all-001"})),
		mutationCommand("approvals.set", []string{"approvals", "set"}, "Grant the official Polymarket V2 trading contracts their required approvals", false, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationApproval, Signing: true, Financial: true, Broadcast: true, Reversible: true, Risk: contract.RiskCritical}, []registry.FieldSpec{walletField(), enumField("scope", true, []string{"POLYMARKET_V2"}, "Pinned protocol approval scope"), clientRequestIDField()}, example("Plan protocol approvals", `pmx approvals set --params '{"scope":"POLYMARKET_V2","clientRequestId":"approve-001"}'`, map[string]any{"scope": "POLYMARKET_V2", "clientRequestId": "approve-001"})),
	}
	for index := range mutations {
		if mutations[index].ID == "approvals.set" {
			mutations[index].Output.HardItemLimit = 11
		}
	}

	wallets := []registry.CommandSpec{
		localMutationCommand("wallet.create", []string{"wallet", "create"}, "Create a secp256k1 wallet in the operating-system keychain", false, contract.MutationCredential, []registry.FieldSpec{profileNameField(true), enumFieldDefault("signatureType", []string{"eoa"}, "eoa", "Wallet signing mode")}, example("Plan wallet creation", `pmx wallet create --params '{"name":"trading"}'`, map[string]any{"name": "trading"})),
		localMutationCommand("wallet.import", []string{"wallet", "import"}, "Import a private key from a masked controlling-terminal prompt", false, contract.MutationCredential, []registry.FieldSpec{profileNameField(true), addressField("expectedAddress", true), enumFieldDefault("signatureType", []string{"eoa"}, "eoa", "Wallet signing mode")}, example("Plan wallet import", `pmx wallet import --params '{"name":"trading","expectedAddress":"0x0000000000000000000000000000000000000000"}'`, map[string]any{"name": "trading", "expectedAddress": "0x0000000000000000000000000000000000000000"})),
		localReadCommand("wallet.list", []string{"wallet", "list"}, "List configured wallet metadata without secrets", nil, true, example("List wallet profiles", "pmx wallet list", map[string]any{})),
		localReadCommand("wallet.show", []string{"wallet", "show"}, "Show one wallet profile without secrets", []registry.FieldSpec{walletField()}, false, example("Show the active wallet", `pmx wallet show --params '{}'`, map[string]any{})),
		localMutationCommand("wallet.use", []string{"wallet", "use"}, "Select the active wallet profile", true, contract.MutationCredential, []registry.FieldSpec{profileNameField(true)}, example("Select a wallet", `pmx wallet use --params '{"name":"trading"}'`, map[string]any{"name": "trading"})),
		localMutationCommand("wallet.remove", []string{"wallet", "remove"}, "Remove a wallet profile and its keychain secret", false, contract.MutationCredential, []registry.FieldSpec{profileNameField(true)}, example("Plan wallet removal", `pmx wallet remove --params '{"name":"trading"}'`, map[string]any{"name": "trading"})),
		localMutationCommand("wallet.sign-message", []string{"wallet", "sign-message"}, "Create an EIP-191 personal-message signature after operator confirmation", false, contract.MutationSignature, []registry.FieldSpec{walletField(), stringField("message", true, 4096, "Exact message to sign"), clientRequestIDField()}, example("Plan message signing", `pmx wallet sign-message --params '{"message":"example.com login nonce 123","clientRequestId":"sign-001"}'`, map[string]any{"message": "example.com login nonce 123", "clientRequestId": "sign-001"})),
	}
	wallets[0].Auth = registry.AuthSpec{Mode: registry.AuthNone, ProfileRequired: false}
	wallets[1].Auth = registry.AuthSpec{Mode: registry.AuthNone, ProfileRequired: false}
	wallets[3].Auth = registry.AuthSpec{Mode: registry.AuthNone, ProfileRequired: true}
	wallets[4].Auth = registry.AuthSpec{Mode: registry.AuthNone, ProfileRequired: true}
	wallets[4].Effects.Effects.Reversible = false

	transactions := []registry.CommandSpec{
		localReadCommand("transactions.inspect", []string{"transactions", "inspect"}, "Decode a bounded signed transaction file without broadcasting", []registry.FieldSpec{rawTransactionFileField(), clientRequestIDFieldOptional()}, false, example("Inspect a signed transaction file", `pmx transactions inspect --params '{"rawTransactionFile":"/secure/path/tx.hex"}'`, map[string]any{"rawTransactionFile": "/secure/path/tx.hex"})),
		mutationCommand("transactions.submit", []string{"transactions", "submit"}, "Submit an already-signed Polygon transaction after operator confirmation", false, contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationOnchain, Signing: false, Financial: true, Broadcast: true, Reversible: false, Risk: contract.RiskCritical}, []registry.FieldSpec{rawTransactionFileField(), enumField("scope", true, []string{"ARBITRARY_POLYGON_TRANSACTION"}, "Required acknowledgement that destination, calldata, and value are unrestricted"), clientRequestIDField()}, example("Plan signed transaction submission", `pmx transactions submit --params '{"rawTransactionFile":"/secure/path/tx.hex","scope":"ARBITRARY_POLYGON_TRANSACTION","clientRequestId":"tx-001"}'`, map[string]any{"rawTransactionFile": "/secure/path/tx.hex", "scope": "ARBITRARY_POLYGON_TRANSACTION", "clientRequestId": "tx-001"})),
	}
	transactions[1].Auth = registry.AuthSpec{Mode: registry.AuthNone, ProfileRequired: false}

	result := append(read, mutations...)
	result = append(result, wallets...)
	return append(result, transactions...)
}

func authReadCommand(id string, path []string, summary string, fields []registry.FieldSpec, sample registry.Example) registry.CommandSpec {
	spec := baseCommand(id, path, summary, true, fields, registry.AuthCLOBTrade, contract.Effects{Network: contract.NetworkRead, Mutation: contract.MutationNone, Signing: true, Risk: contract.RiskLow})
	if id == "orders.list" || id == "trades.list" {
		spec.Response = registry.ValueSpec{Kind: registry.KindObject, Properties: []registry.FieldSpec{
			{Name: "data", Kind: registry.KindArray, Required: true, Items: &registry.ValueSpec{Kind: registry.KindObject, AdditionalProperties: true}},
			{Name: "next_cursor", Kind: registry.KindString, Required: true, MaxBytes: 256},
		}}
		spec.Output.Collection = true
		spec.Output.HardItemLimit = 100
		spec.Pagination = &registry.PaginationSpec{Kind: "cursor", CursorField: "cursor", DefaultMaxItems: 100, HardMaxItems: 100, DefaultMaxPages: 1, HardMaxPages: 1}
	}
	spec.Examples = []registry.Example{sample}
	return spec
}

func approvalCheckCommand() registry.CommandSpec {
	spec := baseCommand("approvals.check", []string{"approvals", "check"}, "Check current Polymarket V2 token approvals", true, []registry.FieldSpec{walletField()}, registry.AuthNone, contract.Effects{Network: contract.NetworkRead, Mutation: contract.MutationNone, Risk: contract.RiskLow})
	spec.Auth.ProfileRequired = true
	spec.Response = arrayOfObjectsResponse()
	spec.Output.Collection = true
	spec.Output.HardItemLimit = 6
	spec.Examples = []registry.Example{example("Check trading approvals", `pmx approvals check --params '{}'`, map[string]any{})}
	return spec
}

func arrayOfObjectsResponse() registry.ValueSpec {
	return registry.ValueSpec{Kind: registry.KindArray, Items: &registry.ValueSpec{Kind: registry.KindObject, AdditionalProperties: true}}
}

func localReadCommand(id string, path []string, summary string, fields []registry.FieldSpec, collection bool, sample registry.Example) registry.CommandSpec {
	spec := baseCommand(id, path, summary, true, fields, registry.AuthNone, contract.Effects{Network: contract.NetworkNone, Mutation: contract.MutationNone, Risk: contract.RiskNone})
	spec.Output.Collection = collection
	spec.Examples = []registry.Example{sample}
	return spec
}

func mutationCommand(id string, path []string, summary string, agentInvocable bool, effects contract.Effects, fields []registry.FieldSpec, sample registry.Example) registry.CommandSpec {
	spec := baseCommand(id, path, summary, agentInvocable, fields, registry.AuthCLOBTrade, effects)
	spec.Effects = registry.EffectSpec{Effects: effects, DryRun: true, Preflight: false, Confirmation: registry.ConfirmationTTY, Idempotent: stringsHasCancel(id)}
	spec.Examples = []registry.Example{sample}
	return spec
}

func localMutationCommand(id string, path []string, summary string, agentInvocable bool, mutation contract.MutationKind, fields []registry.FieldSpec, sample registry.Example) registry.CommandSpec {
	effects := contract.Effects{Network: contract.NetworkNone, Mutation: mutation, Signing: mutation == contract.MutationSignature, Financial: mutation == contract.MutationSignature || mutation == contract.MutationCredential, Broadcast: false, Reversible: mutation == contract.MutationCredential, Risk: contract.RiskCritical}
	spec := baseCommand(id, path, summary, agentInvocable, fields, registry.AuthSigner, effects)
	spec.Effects = registry.EffectSpec{Effects: effects, DryRun: true, Confirmation: registry.ConfirmationTTY, Idempotent: false}
	spec.Examples = []registry.Example{sample}
	return spec
}

func baseCommand(id string, path []string, summary string, agentInvocable bool, fields []registry.FieldSpec, auth registry.AuthMode, effects contract.Effects) registry.CommandSpec {
	return registry.CommandSpec{
		ID: id, Path: path, Summary: summary, AgentInvocable: agentInvocable,
		Params:   registry.ObjectSpec{MaximumBytes: 64 << 10, AdditionalProperties: false, OutputControls: []string{"--json", "--compact", "--fields", "--execute"}, Fields: fields},
		Response: registry.ValueSpec{Kind: registry.KindObject, AdditionalProperties: true},
		Auth:     registry.AuthSpec{Mode: auth, ProfileRequired: auth != registry.AuthNone},
		Effects:  registry.EffectSpec{Effects: effects, Confirmation: registry.ConfirmationNone, Idempotent: true},
		Output:   registry.OutputSpec{Formats: []string{"json"}, MaximumProviderBytes: 8 << 20, MaximumEncodedOutputBytes: 8 << 20},
	}
}

func authenticatedListFields() []registry.FieldSpec {
	return []registry.FieldSpec{walletField(), conditionField("market", false), tokenFieldOptional(), stringField("cursor", false, 256, "Provider pagination cursor")}
}

func walletField() registry.FieldSpec { return walletNameField(false) }

func walletNameField(required bool) registry.FieldSpec {
	return registry.FieldSpec{Name: "wallet", Flag: "wallet", Kind: registry.KindString, Required: required, Pattern: `^[a-z][a-z0-9_-]{0,31}$`, MaxBytes: 32, Normalize: registry.NormalizeLowercase, Description: "Wallet profile name; defaults to the active wallet"}
}

func profileNameField(required bool) registry.FieldSpec {
	return registry.FieldSpec{Name: "name", Flag: "name", Kind: registry.KindString, Required: required, Pattern: `^[a-z][a-z0-9_-]{0,31}$`, MaxBytes: 32, Normalize: registry.NormalizeLowercase, Description: "Wallet profile name"}
}

func addressField(name string, required bool) registry.FieldSpec {
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindString, Required: required, Pattern: `^0x[0-9a-fA-F]{40}$`, MaxBytes: 42, Description: "Ethereum address"}
}

func conditionField(name string, required bool) registry.FieldSpec {
	return registry.FieldSpec{Name: name, Flag: name, Kind: registry.KindString, Required: required, Pattern: `^0x[0-9a-fA-F]{64}$`, MaxBytes: 66, Normalize: registry.NormalizeLowercase, Description: "Condition ID"}
}

func tokenFieldOptional() registry.FieldSpec {
	field := tokenField()
	field.Required = false
	return field
}

func orderIDField(name string, required bool) registry.FieldSpec {
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindString, Required: required, Pattern: `^0x[0-9a-fA-F]{64}$`, MaxBytes: 66, Normalize: registry.NormalizeLowercase, Description: "CLOB order ID"}
}

func orderIDsField() registry.FieldSpec {
	return registry.FieldSpec{Name: "orderIds", Kind: registry.KindArray, Required: true, MaxItems: 100, Items: &registry.ValueSpec{Kind: registry.KindString, Pattern: `^0x[0-9a-fA-F]{64}$`, MaxBytes: 66}, Description: "Order IDs to cancel"}
}

func clientRequestIDField() registry.FieldSpec {
	return registry.FieldSpec{Name: "clientRequestId", Flag: "client-request-id", Kind: registry.KindString, Required: true, Pattern: `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`, MaxBytes: 128, Description: "Caller-generated reconciliation key; it does not make an uncertain mutation safe to retry"}
}

func clientRequestIDFieldOptional() registry.FieldSpec {
	field := clientRequestIDField()
	field.Required = false
	return field
}

func decimalField(name string, required bool, pattern, description string) registry.FieldSpec {
	return registry.FieldSpec{Name: name, Flag: flagName(name), Kind: registry.KindString, Required: required, Pattern: pattern, MaxBytes: 64, Normalize: registry.NormalizeTrim, Description: description}
}

func positiveDecimalField(name, description string) registry.FieldSpec {
	return decimalField(name, true, `^(?:0\.[0-9]{1,6}|[1-9][0-9]*(?:\.[0-9]{1,6})?)$`, description)
}

func priceField() registry.FieldSpec {
	return decimalField("price", true, `^(?:1(?:\.0{1,6})?|0\.(?:[1-9][0-9]{0,5}|0[1-9][0-9]{0,4}|00[1-9][0-9]{0,3}|000[1-9][0-9]{0,2}|0000[1-9][0-9]?|00000[1-9]))$`, "Price above 0 through 1 with at most six decimals")
}

func enumFieldDefault(name string, values []string, defaultValue, description string) registry.FieldSpec {
	field := enumField(name, false, values, description)
	field.Default = defaultValue
	return field
}

func rawTransactionFileField() registry.FieldSpec {
	return registry.FieldSpec{Name: "rawTransactionFile", Flag: "raw-transaction-file", Kind: registry.KindString, Required: true, MaxBytes: 4096, Normalize: registry.NormalizeTrim, Description: "Path to a bounded signed-transaction file; signed bytes are never accepted in argv or --params"}
}

func example(summary, command string, parameters any) registry.Example {
	return registry.Example{Summary: summary, Command: command, Params: parameters}
}

func zeroHash() string { return "0x0000000000000000000000000000000000000000000000000000000000000000" }

func stringsHasCancel(id string) bool {
	return id == "orders.cancel" || id == "orders.cancel-batch" || id == "orders.cancel-market" || id == "orders.cancel-all"
}
