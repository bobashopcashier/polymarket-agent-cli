package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/sanitize"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/transaction"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/upstream"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/wallet"
)

type planDocument struct {
	Command                string                       `json:"command"`
	Request                any                          `json:"request"`
	Transaction            *transaction.Inspection      `json:"transaction,omitempty"`
	Wallet                 *wallet.PublicWallet         `json:"wallet,omitempty"`
	ExecutionEngine        string                       `json:"executionEngine,omitempty"`
	ExecutionEngineSHA256  string                       `json:"executionEngineSha256,omitempty"`
	EngineRequiredRevision string                       `json:"executionEngineRequiredRevision,omitempty"`
	EngineRevisionAttested *bool                        `json:"executionEngineRevisionAttested,omitempty"`
	ApprovalOperations     []upstream.ApprovalOperation `json:"approvalOperations,omitempty"`
	ApprovalSourceRevision string                       `json:"approvalOperationsSourceRevision,omitempty"`
	OrderExposure          *orderExposure               `json:"orderExposure,omitempty"`
	Effects                contract.Effects             `json:"effects"`
	RequiresControllingTTY bool                         `json:"requiresControllingTty"`
	Fingerprint            string                       `json:"fingerprint"`
}

type orderExposure struct {
	Asset            string `json:"asset"`
	ComputedNotional string `json:"computedNotional"`
	CallerMaximum    string `json:"callerMaximum"`
	PostOnly         bool   `json:"postOnly"`
}

func isManagedRead(id string) bool {
	switch id {
	case "auth.status", "wallet.list", "wallet.show", "transactions.inspect",
		"orders.list", "orders.get", "trades.list", "balances.get", "approvals.check":
		return true
	default:
		return false
	}
}

func (a *App) executeManagedRead(ctx context.Context, invocation invocation) (any, contract.Meta, error) {
	switch invocation.Command.ID {
	case "auth.status":
		wallets, err := a.wallets.List(ctx)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		var active wallet.PublicWallet
		if len(wallets) != 0 {
			active, err = a.wallets.Active(ctx)
			if err != nil && !errors.Is(err, wallet.ErrNoActiveWallet) {
				return nil, contract.Meta{}, err
			}
		}
		configured := len(wallets) != 0 && active.Name != ""
		secretReady := false
		walletError := ""
		if configured {
			_, checkErr := a.wallets.Check(ctx, active.Name)
			secretReady = checkErr == nil
			walletError = walletReadinessCode(checkErr)
		}
		upstreamConfigured := a.upstream != nil
		data := map[string]any{
			"walletConfigured":   configured,
			"walletSecretReady":  secretReady,
			"upstreamConfigured": upstreamConfigured,
			"ready":              configured && secretReady && upstreamConfigured && active.SignatureType == wallet.SignatureEOA,
			"protocol":           "polymarket-clob-v2",
			"chainId":            transaction.PolygonChainID,
		}
		if configured {
			data["wallet"] = active.Name
			data["address"] = active.Address
			data["signatureType"] = active.SignatureType
		}
		if walletError != "" {
			data["walletError"] = walletError
		}
		if upstreamConfigured {
			data["upstreamPath"] = a.upstream.ExecutablePath()
			data["upstreamSha256"] = a.upstream.ExecutableSHA256()
			data["upstreamRequiredRevision"] = upstream.RequiredRevision
			data["upstreamRevisionAttested"] = false
		}
		return sanitized(data, localReadMeta())
	case "wallet.list":
		wallets, err := a.wallets.List(ctx)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		active := ""
		if current, activeErr := a.wallets.Active(ctx); activeErr == nil {
			active = current.Name
		} else if !errors.Is(activeErr, wallet.ErrNoActiveWallet) {
			return nil, contract.Meta{}, activeErr
		}
		return sanitized(map[string]any{"active": active, "wallets": wallets}, localReadMeta())
	case "wallet.show":
		request := invocation.Request.(*walletSelectionRequest)
		entry, err := a.wallets.Show(ctx, request.Wallet)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		return sanitized(a.publicWalletResult(ctx, entry), localReadMeta())
	case "transactions.inspect":
		request := invocation.Request.(*rawTransactionRequest)
		parsed, err := transaction.ParseFile(request.RawTransactionFile)
		if err != nil {
			return nil, contract.Meta{}, contract.Invalid("invalid_signed_transaction", err.Error())
		}
		defer parsed.Close()
		return sanitized(parsed.Inspection, localReadMeta())
	case "approvals.check":
		if a.upstream == nil {
			return nil, contract.Meta{}, upstreamUnavailable()
		}
		request := invocation.Request.(*walletSelectionRequest)
		entry, err := a.wallets.Show(ctx, request.Wallet)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		command, err := upstream.BuildApprovalCheck(entry.Address)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		result, err := a.upstream.Run(ctx, command, nil)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		return a.decodeUpstreamResult(invocation, result, false)
	default:
		command, walletName, err := buildAuthenticatedRead(invocation)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		result, err := a.runUpstreamWithWallet(ctx, walletName, nil, command)
		if err != nil {
			return nil, contract.Meta{}, err
		}
		return a.decodeUpstreamResult(invocation, result, false)
	}
}

func (a *App) executeMutation(ctx context.Context, invocation invocation) (any, contract.Meta, error) {
	var parsed *transaction.Parsed
	if invocation.Command.ID == "transactions.submit" {
		request := invocation.Request.(*rawTransactionSubmitRequest)
		var err error
		parsed, err = transaction.ParseFile(request.RawTransactionFile)
		if err != nil {
			return nil, contract.Meta{}, contract.Invalid("invalid_signed_transaction", err.Error())
		}
		defer parsed.Close()
		if parsed.Inspection.ChainID != "137" {
			return nil, contract.Meta{}, contract.Invalid("wrong_chain", "signed transaction chain ID must be Polygon 137")
		}
	}

	plan, phrase, summary, err := a.buildMutationPlan(ctx, invocation, parsed)
	if err != nil {
		return nil, contract.Meta{}, err
	}
	clientRequestID := mutationClientRequestID(invocation.Request)
	if !invocation.Options.Execute {
		meta := mutationMeta(invocation, false, "planned", clientRequestID, plan.Fingerprint)
		return map[string]any{"dryRun": true, "executes": false, "plan": plan}, meta, nil
	}
	if err := a.console.Confirm(ctx, summary, phrase); err != nil {
		if ctx.Err() != nil {
			return nil, contract.Meta{}, ctx.Err()
		}
		return nil, contract.Meta{}, contract.PolicyDenied("human_authorization_required", "live execution requires a matching authorization phrase on the controlling terminal").WithCause(err)
	}

	data, err := a.executeConfirmedMutation(ctx, invocation, parsed, plan.Wallet)
	if err != nil {
		return nil, contract.Meta{}, err
	}
	meta := mutationMeta(invocation, true, "accepted", clientRequestID, plan.Fingerprint)
	return map[string]any{"dryRun": false, "executes": true, "result": data}, meta, nil
}

func (a *App) buildMutationPlan(ctx context.Context, invocation invocation, parsed *transaction.Parsed) (planDocument, string, string, error) {
	if invocation.Options.Execute && usesUpstreamMutation(invocation.Command.ID) && a.upstream == nil {
		return planDocument{}, "", "", upstreamUnavailable()
	}
	request, err := jsonValue(invocation.Request)
	if err != nil {
		return planDocument{}, "", "", err
	}
	effects := invocation.Command.Effects.Effects
	effects.Executed = false
	planWallet, err := a.resolvePlanWallet(ctx, invocation)
	if err != nil {
		return planDocument{}, "", "", err
	}
	exposure, err := mutationExposure(invocation.Request)
	if err != nil {
		return planDocument{}, "", "", err
	}
	unsigned := struct {
		Command         string                       `json:"command"`
		Request         any                          `json:"request"`
		Transaction     *transaction.Inspection      `json:"transaction,omitempty"`
		Wallet          *wallet.PublicWallet         `json:"wallet,omitempty"`
		ExecutionEngine string                       `json:"executionEngine,omitempty"`
		EngineSHA256    string                       `json:"executionEngineSha256,omitempty"`
		EngineRevision  string                       `json:"executionEngineRequiredRevision,omitempty"`
		EngineAttested  *bool                        `json:"executionEngineRevisionAttested,omitempty"`
		Approvals       []upstream.ApprovalOperation `json:"approvalOperations,omitempty"`
		ApprovalSource  string                       `json:"approvalOperationsSourceRevision,omitempty"`
		OrderExposure   *orderExposure               `json:"orderExposure,omitempty"`
		Effects         contract.Effects             `json:"effects"`
	}{Command: invocation.Command.ID, Request: request, Wallet: planWallet, OrderExposure: exposure, Effects: effects}
	if parsed != nil {
		inspection := parsed.Inspection
		unsigned.Transaction = &inspection
	}
	if usesUpstreamMutation(invocation.Command.ID) && a.upstream != nil {
		unsigned.ExecutionEngine = a.upstream.ExecutablePath()
		unsigned.EngineSHA256 = a.upstream.ExecutableSHA256()
		unsigned.EngineRevision = upstream.RequiredRevision
		attested := false
		unsigned.EngineAttested = &attested
	}
	if invocation.Command.ID == "approvals.set" {
		unsigned.Approvals = upstream.ApprovalOperations()
		unsigned.ApprovalSource = upstream.RequiredRevision
	}
	encoded, err := json.Marshal(unsigned)
	if err != nil {
		return planDocument{}, "", "", contract.Internal("could not encode the operation plan", err)
	}
	digest := sha256.Sum256(encoded)
	fingerprint := "sha256:" + hex.EncodeToString(digest[:])
	plan := planDocument{
		Command: invocation.Command.ID, Request: request, Transaction: unsigned.Transaction, Wallet: planWallet,
		ExecutionEngine: unsigned.ExecutionEngine, ExecutionEngineSHA256: unsigned.EngineSHA256,
		EngineRequiredRevision: unsigned.EngineRevision, EngineRevisionAttested: unsigned.EngineAttested,
		ApprovalOperations:     unsigned.Approvals,
		ApprovalSourceRevision: unsigned.ApprovalSource,
		OrderExposure:          unsigned.OrderExposure,
		Effects:                effects, RequiresControllingTTY: true, Fingerprint: fingerprint,
	}
	visible, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return planDocument{}, "", "", contract.Internal("could not render the operation plan", err)
	}
	phrase := "execute-" + hex.EncodeToString(digest[:6])
	summary := "Review this pmx operation before authorizing:\n" + string(visible)
	return plan, phrase, summary, nil
}

func mutationExposure(request any) (*orderExposure, error) {
	order, ok := request.(*limitOrderRequest)
	if !ok {
		return nil, nil
	}
	price, priceOK := new(big.Rat).SetString(order.Price)
	size, sizeOK := new(big.Rat).SetString(order.Size)
	maximum, maximumOK := new(big.Rat).SetString(order.MaxNotionalPUSD)
	if !priceOK || !sizeOK || !maximumOK || price.Sign() <= 0 || size.Sign() <= 0 || maximum.Sign() <= 0 {
		return nil, contract.Invalid("invalid_order_exposure", "price, size, and maxNotionalPusd must be positive decimal values")
	}
	notional := new(big.Rat).Mul(price, size)
	if notional.Cmp(maximum) > 0 {
		return nil, contract.Invalid("order_notional_exceeded", "price times size exceeds maxNotionalPusd").WithDetails(map[string]any{
			"computedNotional": exactDecimal(notional, 12), "maxNotionalPusd": order.MaxNotionalPUSD,
		})
	}
	return &orderExposure{
		Asset: "pUSD", ComputedNotional: exactDecimal(notional, 12),
		CallerMaximum: order.MaxNotionalPUSD, PostOnly: true,
	}, nil
}

func exactDecimal(value *big.Rat, precision int) string {
	formatted := value.FloatString(precision)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}

func (a *App) resolvePlanWallet(ctx context.Context, invocation invocation) (*wallet.PublicWallet, error) {
	name, required := mutationWalletName(invocation.Request)
	if !required {
		return nil, nil
	}
	entry, err := a.wallets.Show(ctx, name)
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func mutationWalletName(request any) (string, bool) {
	switch typed := request.(type) {
	case *limitOrderRequest:
		return typed.Wallet, true
	case *cancelOrderRequest:
		return typed.Wallet, true
	case *cancelBatchRequest:
		return typed.Wallet, true
	case *cancelMarketRequest:
		return typed.Wallet, true
	case *cancelAllRequest:
		return typed.Wallet, true
	case *approvalRequest:
		return typed.Wallet, true
	case *signMessageRequest:
		return typed.Wallet, true
	case *walletNameRequest:
		return typed.Name, true
	default:
		return "", false
	}
}

func usesUpstreamMutation(id string) bool {
	switch id {
	case "orders.create", "orders.cancel", "orders.cancel-batch", "orders.cancel-market", "orders.cancel-all", "approvals.set":
		return true
	default:
		return false
	}
}

func (a *App) executeConfirmedMutation(ctx context.Context, invocation invocation, parsed *transaction.Parsed, plannedWallet *wallet.PublicWallet) (any, error) {
	switch invocation.Command.ID {
	case "wallet.create":
		request := invocation.Request.(*walletCreateRequest)
		entry, err := a.wallets.Generate(ctx, wallet.GenerateOptions{Name: request.Name, SignatureType: wallet.SignatureEOA})
		if err != nil {
			return nil, err
		}
		return sanitize.Value(a.publicWalletResult(ctx, entry))
	case "wallet.import":
		request := invocation.Request.(*walletImportRequest)
		secret, err := a.console.ReadSecret(ctx, "Private key (input hidden): ", 128)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, contract.PolicyDenied("secret_input_unavailable", "private-key import requires masked input from the controlling terminal").WithCause(err)
		}
		defer zeroBytes(secret)
		entry, err := a.wallets.Import(ctx, wallet.ImportOptions{
			Name: request.Name, ExpectedAddress: request.ExpectedAddress, SignatureType: wallet.SignatureEOA,
		}, secret)
		if err != nil {
			return nil, err
		}
		return sanitize.Value(a.publicWalletResult(ctx, entry))
	case "wallet.use":
		if plannedWallet == nil {
			return nil, contract.Internal("wallet plan identity is missing", nil)
		}
		entry, err := a.wallets.UseExpected(ctx, *plannedWallet)
		if err != nil {
			return nil, err
		}
		return sanitize.Value(a.publicWalletResult(ctx, entry))
	case "wallet.remove":
		if plannedWallet == nil {
			return nil, contract.Internal("wallet plan identity is missing", nil)
		}
		if err := a.wallets.RemoveExpected(ctx, *plannedWallet); err != nil {
			return nil, err
		}
		return sanitize.Value(map[string]any{"name": plannedWallet.Name, "address": plannedWallet.Address, "removed": true})
	case "wallet.sign-message":
		request := invocation.Request.(*signMessageRequest)
		if plannedWallet == nil {
			return nil, contract.Internal("wallet plan identity is missing", nil)
		}
		signer, err := a.wallets.GetSignerExpected(ctx, *plannedWallet)
		if err != nil {
			return nil, err
		}
		defer signer.Destroy()
		digest := accounts.TextHash([]byte(request.Message))
		signature, err := signer.SignDigest(digest)
		if err != nil {
			return nil, err
		}
		defer zeroBytes(signature)
		signature[64] += 27
		return sanitize.Value(map[string]any{
			"address": signer.Address(), "messageHash": "0x" + hex.EncodeToString(digest),
			"signature": "0x" + hex.EncodeToString(signature), "mode": "eip-191",
		})
	case "transactions.submit":
		if parsed == nil {
			return nil, contract.Internal("signed transaction plan was not retained", nil)
		}
		hash, err := a.txSender.Submit(ctx, parsed)
		if err != nil {
			return nil, indeterminateMutation("Polygon transaction submission could not be confirmed", err)
		}
		return sanitize.Value(map[string]any{"submitted": true, "hash": hash, "transaction": parsed.Inspection})
	default:
		if a.upstream == nil {
			return nil, upstreamUnavailable()
		}
		command, walletName, err := buildMutationCommand(invocation)
		if err != nil {
			return nil, err
		}
		if plannedWallet == nil {
			return nil, contract.Internal("wallet plan identity is missing", nil)
		}
		result, err := a.runUpstreamWithWallet(ctx, walletName, plannedWallet, command)
		if err != nil {
			return nil, indeterminateMutation("Polymarket mutation could not be confirmed", err)
		}
		data, _, err := a.decodeUpstreamResult(invocation, result, true)
		return data, err
	}
}

func buildAuthenticatedRead(invocation invocation) (upstream.Command, string, error) {
	switch invocation.Command.ID {
	case "orders.list":
		request := invocation.Request.(*authenticatedListRequest)
		command, err := upstream.BuildOpenOrders(upstream.OrdersQuery{MarketConditionID: request.Market, AssetTokenID: request.Token, Cursor: request.Cursor})
		return command, request.Wallet, err
	case "orders.get":
		request := invocation.Request.(*authenticatedIDRequest)
		command, err := upstream.BuildOrderRead(request.ID)
		return command, request.Wallet, err
	case "trades.list":
		request := invocation.Request.(*authenticatedListRequest)
		command, err := upstream.BuildTrades(upstream.OrdersQuery{MarketConditionID: request.Market, AssetTokenID: request.Token, Cursor: request.Cursor})
		return command, request.Wallet, err
	case "balances.get":
		request := invocation.Request.(*balanceRequest)
		command, err := upstream.BuildBalance(upstream.BalanceQuery{AssetType: upstream.AssetType(request.AssetType), TokenID: request.TokenID})
		return command, request.Wallet, err
	default:
		return upstream.Command{}, "", contract.Internal("authenticated command has no execution mapping", nil)
	}
}

func buildMutationCommand(invocation invocation) (upstream.Command, string, error) {
	switch invocation.Command.ID {
	case "orders.create":
		request := invocation.Request.(*limitOrderRequest)
		command, err := upstream.BuildLimitOrder(upstream.LimitOrder{
			TokenID: request.TokenID, Side: upstream.Side(strings.ToLower(request.Side)), Price: request.Price,
			Size: request.Size, Type: upstream.OrderType(request.OrderType), PostOnly: true,
		})
		return command, request.Wallet, err
	case "orders.cancel":
		request := invocation.Request.(*cancelOrderRequest)
		command, err := upstream.BuildCancelOrder(request.OrderID)
		return command, request.Wallet, err
	case "orders.cancel-batch":
		request := invocation.Request.(*cancelBatchRequest)
		command, err := upstream.BuildCancelOrders(request.OrderIDs)
		return command, request.Wallet, err
	case "orders.cancel-market":
		request := invocation.Request.(*cancelMarketRequest)
		command, err := upstream.BuildCancelMarket(request.Market, request.TokenID)
		return command, request.Wallet, err
	case "orders.cancel-all":
		request := invocation.Request.(*cancelAllRequest)
		return upstream.BuildCancelAll(), request.Wallet, nil
	case "approvals.set":
		request := invocation.Request.(*approvalRequest)
		return upstream.BuildApprovalSet(), request.Wallet, nil
	default:
		return upstream.Command{}, "", contract.Internal("mutation command has no execution mapping", nil)
	}
}

func (a *App) runUpstreamWithWallet(ctx context.Context, walletName string, expected *wallet.PublicWallet, command upstream.Command) (upstream.Result, error) {
	if a.upstream == nil {
		return upstream.Result{}, upstreamUnavailable()
	}
	var result upstream.Result
	callback := func(entry wallet.PublicWallet, secret []byte) error {
		if entry.SignatureType != wallet.SignatureEOA {
			return contract.PolicyDenied("unsupported_wallet_type", "this release executes authenticated commands only with EOA wallet profiles")
		}
		var runErr error
		result, runErr = a.upstream.Run(ctx, command, &upstream.Credentials{PrivateKey: secret, SignatureType: upstream.SignatureEOA})
		return runErr
	}
	var err error
	if expected != nil {
		err = a.wallets.WithSecretExpected(ctx, *expected, callback)
	} else {
		err = a.wallets.WithSecret(ctx, walletName, callback)
	}
	return result, err
}

func (a *App) decodeUpstreamResult(invocation invocation, result upstream.Result, mutation bool) (any, contract.Meta, error) {
	data, err := decodeRaw(result.Raw)
	if err != nil {
		return nil, contract.Meta{}, err
	}
	data, err = sanitize.Value(data)
	if err != nil {
		return nil, contract.Meta{}, err
	}
	effects := invocation.Command.Effects.Effects
	effects.Executed = true
	if !mutation {
		effects.Network = contract.NetworkRead
		effects.Mutation = contract.MutationNone
	}
	data, truncation := boundResult(invocation.Command, data)
	meta := contract.Meta{Provider: "polymarket-official-cli", Effects: effects, Truncation: truncation}
	if invocation.Command.ID == "orders.list" || invocation.Command.ID == "trades.list" {
		meta.Pagination = authenticatedPagination(data, len(truncation) != 0)
	}
	return data, meta, nil
}

func authenticatedPagination(data any, truncated bool) *contract.Pagination {
	document, ok := data.(map[string]any)
	if !ok {
		return &contract.Pagination{PagesFetched: 1, Complete: false}
	}
	cursor, _ := document["next_cursor"].(string)
	complete := cursor == "LTE=" && !truncated
	pagination := &contract.Pagination{PagesFetched: 1, ItemsEmitted: collectionCount(data), Complete: complete}
	if !complete && !truncated {
		pagination.NextCursor = cursor
	}
	return pagination
}

func (a *App) publicWalletResult(ctx context.Context, entry wallet.PublicWallet) map[string]any {
	active := false
	if current, err := a.wallets.Active(ctx); err == nil {
		active = current.Name == entry.Name
	}
	_, checkErr := a.wallets.Check(ctx, entry.Name)
	return map[string]any{
		"name": entry.Name, "address": entry.Address, "signatureType": entry.SignatureType,
		"funder": entry.Funder, "active": active, "stored": checkErr == nil,
	}
}

func walletReadinessCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, wallet.ErrSecretNotFound):
		return "secret_not_found"
	case errors.Is(err, wallet.ErrInvalidSecret):
		return "invalid_secret"
	case errors.Is(err, wallet.ErrAddressMismatch), errors.Is(err, wallet.ErrWalletChanged):
		return "address_mismatch"
	case errors.Is(err, wallet.ErrKeyringUnavailable), errors.Is(err, wallet.ErrSecretStore):
		return "keychain_unavailable"
	default:
		return "wallet_unavailable"
	}
}

func jsonValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, contract.Internal("could not encode the validated request", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, contract.Internal("could not decode the validated request", err)
	}
	return sanitize.Value(result)
}

func sanitized(data any, meta contract.Meta) (any, contract.Meta, error) {
	safe, err := sanitize.Value(data)
	return safe, meta, err
}

func localReadMeta() contract.Meta {
	return contract.Meta{Provider: "pmx-local", Effects: contract.Effects{Executed: true, Network: contract.NetworkNone, Mutation: contract.MutationNone, Risk: contract.RiskNone}}
}

func mutationMeta(invocation invocation, executed bool, status, clientRequestID, operationID string) contract.Meta {
	effects := invocation.Command.Effects.Effects
	effects.Executed = executed
	return contract.Meta{
		Provider: "pmx", Effects: effects,
		Operation: &contract.Operation{OperationID: operationID, ClientRequestID: clientRequestID, Status: status},
	}
}

func mutationClientRequestID(request any) string {
	switch typed := request.(type) {
	case *limitOrderRequest:
		return typed.ClientRequestID
	case *cancelOrderRequest:
		return typed.ClientRequestID
	case *cancelBatchRequest:
		return typed.ClientRequestID
	case *cancelMarketRequest:
		return typed.ClientRequestID
	case *cancelAllRequest:
		return typed.ClientRequestID
	case *approvalRequest:
		return typed.ClientRequestID
	case *signMessageRequest:
		return typed.ClientRequestID
	case *rawTransactionRequest:
		return typed.ClientRequestID
	case *rawTransactionSubmitRequest:
		return typed.ClientRequestID
	default:
		return ""
	}
}

func upstreamUnavailable() *contract.Error {
	return contract.PolicyDenied("upstream_not_configured", "the official Polymarket CLI is not configured").WithHint("Install polymarket-cli or set PMX_POLYMARKET_BIN to its absolute executable path")
}

func indeterminateMutation(message string, cause error) *contract.Error {
	return contract.NewError("mutation_indeterminate", contract.CategoryIndeterminate, message, contract.ExitIndeterminate).
		WithHint("Do not retry blindly; reconcile orders, approvals, or the transaction hash first").WithCause(cause)
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
