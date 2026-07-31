package upstream

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

func (s Side) valid() bool { return s == SideBuy || s == SideSell }

type OrderType string

const (
	OrderGTC OrderType = "GTC"
)

type AssetType string

const (
	AssetCollateral  AssetType = "collateral"
	AssetConditional AssetType = "conditional"
)

type LimitOrder struct {
	TokenID  string
	Side     Side
	Price    string
	Size     string
	Type     OrderType
	PostOnly bool
}

type OrdersQuery struct {
	MarketConditionID string
	AssetTokenID      string
	Cursor            string
}

type BalanceQuery struct {
	AssetType AssetType
	TokenID   string
}

var (
	decimalPattern    = regexp.MustCompile(`^(0|[1-9][0-9]{0,77})(\.[0-9]{1,18})?$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:~-]+$`)
	cursorPattern     = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)
	addressPattern    = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	conditionPattern  = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

func BuildLimitOrder(request LimitOrder) (Command, error) {
	if err := validateTokenID(request.TokenID); err != nil {
		return Command{}, invalidCommand("token ID: " + err.Error())
	}
	if !request.Side.valid() {
		return Command{}, invalidCommand("side must be buy or sell")
	}
	if err := validateDecimal(request.Price, false); err != nil {
		return Command{}, invalidCommand("price: " + err.Error())
	}
	price, _ := new(big.Rat).SetString(request.Price)
	if price.Cmp(big.NewRat(1, 1)) > 0 {
		return Command{}, invalidCommand("price must not exceed 1")
	}
	if err := validateDecimal(request.Size, true); err != nil {
		return Command{}, invalidCommand("size: " + err.Error())
	}
	if request.Type != OrderGTC {
		return Command{}, invalidCommand("limit order type must be GTC")
	}
	if !request.PostOnly {
		return Command{}, invalidCommand("limit orders must be post-only")
	}

	args := []string{
		"clob", "create-order",
		"--token", request.TokenID,
		"--side", string(request.Side),
		"--price", request.Price,
		"--size", request.Size,
		"--order-type", string(request.Type),
	}
	args = append(args, "--post-only")
	return newCommand("clob.create_order", true, args...), nil
}

func BuildCancelOrder(orderID string) (Command, error) {
	if err := validateIdentifier("order ID", orderID); err != nil {
		return Command{}, err
	}
	return newCommand("clob.cancel", true, "clob", "cancel", orderID), nil
}

func BuildCancelOrders(orderIDs []string) (Command, error) {
	if len(orderIDs) == 0 || len(orderIDs) > 100 {
		return Command{}, invalidCommand("order IDs must contain between 1 and 100 items")
	}
	for _, orderID := range orderIDs {
		if err := validateIdentifier("order ID", orderID); err != nil {
			return Command{}, err
		}
	}
	joined := strings.Join(orderIDs, ",")
	if len(joined) > 16<<10 {
		return Command{}, invalidCommand("combined order IDs are too long")
	}
	return newCommand("clob.cancel_orders", true, "clob", "cancel-orders", joined), nil
}

func BuildCancelAll() Command {
	return newCommand("clob.cancel_all", true, "clob", "cancel-all")
}

func BuildCancelMarket(marketConditionID, assetTokenID string) (Command, error) {
	if marketConditionID == "" && assetTokenID == "" {
		return Command{}, invalidCommand("market condition ID or asset token ID is required")
	}
	args := []string{"clob", "cancel-market"}
	if marketConditionID != "" {
		if !conditionPattern.MatchString(marketConditionID) {
			return Command{}, invalidCommand("market condition ID must be 0x-prefixed 32-byte hex")
		}
		args = append(args, "--market", marketConditionID)
	}
	if assetTokenID != "" {
		if err := validateTokenID(assetTokenID); err != nil {
			return Command{}, invalidCommand("asset token ID: " + err.Error())
		}
		args = append(args, "--asset", assetTokenID)
	}
	return newCommand("clob.cancel_market", true, args...), nil
}

// BuildApprovalCheck checks on-chain allowances. Supplying an address makes
// the operation public; omitting it requires credentials so the official CLI
// can resolve the configured account wallet.
func BuildApprovalCheck(address string) (Command, error) {
	args := []string{"approve", "check"}
	requiresSecret := address == ""
	if address != "" {
		if !addressPattern.MatchString(address) {
			return Command{}, invalidCommand("address must be 0x-prefixed 20-byte hex")
		}
		args = append(args, address)
	}
	return newCommand("approve.check", requiresSecret, args...), nil
}

// BuildApprovalSet invokes the official CLI's blanket trading-approval flow.
// Callers must treat it as a critical, policy-gated financial mutation.
func BuildApprovalSet() Command {
	return newCommand("approve.set", true, "approve", "set")
}

func BuildOpenOrders(query OrdersQuery) (Command, error) {
	args, err := appendOrdersQuery([]string{"clob", "orders"}, query)
	if err != nil {
		return Command{}, err
	}
	return newCommand("clob.orders", true, args...), nil
}

func BuildOrderRead(orderID string) (Command, error) {
	if err := validateIdentifier("order ID", orderID); err != nil {
		return Command{}, err
	}
	return newCommand("clob.order", true, "clob", "order", orderID), nil
}

func BuildTrades(query OrdersQuery) (Command, error) {
	args, err := appendOrdersQuery([]string{"clob", "trades"}, query)
	if err != nil {
		return Command{}, err
	}
	return newCommand("clob.trades", true, args...), nil
}

func BuildBalance(query BalanceQuery) (Command, error) {
	if query.AssetType != AssetCollateral && query.AssetType != AssetConditional {
		return Command{}, invalidCommand("asset type must be collateral or conditional")
	}
	if query.AssetType == AssetCollateral && query.TokenID != "" {
		return Command{}, invalidCommand("collateral balance must not specify a token ID")
	}
	if query.AssetType == AssetConditional && query.TokenID == "" {
		return Command{}, invalidCommand("conditional balance requires a token ID")
	}
	args := []string{"clob", "balance", "--asset-type", string(query.AssetType)}
	if query.TokenID != "" {
		if err := validateTokenID(query.TokenID); err != nil {
			return Command{}, invalidCommand("token ID: " + err.Error())
		}
		args = append(args, "--token", query.TokenID)
	}
	return newCommand("clob.balance", true, args...), nil
}

func BuildAccountStatus() Command {
	return newCommand("clob.account_status", true, "clob", "account-status")
}

func appendOrdersQuery(args []string, query OrdersQuery) ([]string, error) {
	if query.MarketConditionID != "" {
		if !conditionPattern.MatchString(query.MarketConditionID) {
			return nil, invalidCommand("market condition ID must be 0x-prefixed 32-byte hex")
		}
		args = append(args, "--market", query.MarketConditionID)
	}
	if query.AssetTokenID != "" {
		if err := validateTokenID(query.AssetTokenID); err != nil {
			return nil, invalidCommand("asset token ID: " + err.Error())
		}
		args = append(args, "--asset", query.AssetTokenID)
	}
	if query.Cursor != "" {
		if len(query.Cursor) > 512 || strings.HasPrefix(query.Cursor, "-") || !cursorPattern.MatchString(query.Cursor) {
			return nil, invalidCommand("cursor contains unsupported characters or is too long")
		}
		args = append(args, "--cursor", query.Cursor)
	}
	return args, nil
}

func newCommand(operation string, requiresSecret bool, args ...string) Command {
	return Command{operation: operation, args: append([]string(nil), args...), requiresSecret: requiresSecret}
}

func invalidCommand(message string) *Error {
	return &Error{Kind: ErrorInvalidCommand, Message: message}
}

func validateTokenID(value string) error {
	if value == "" || len(value) > 78 {
		return fmt.Errorf("must contain between 1 and 78 decimal digits")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return fmt.Errorf("must contain decimal digits only")
		}
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(value, 10); !ok || parsed.BitLen() > 256 {
		return fmt.Errorf("must fit in an unsigned 256-bit integer")
	}
	return nil
}

func validateDecimal(value string, positive bool) error {
	if len(value) > 97 || !decimalPattern.MatchString(value) {
		return fmt.Errorf("must be a plain non-negative decimal with at most 18 fractional digits")
	}
	if positive {
		parsed, _ := new(big.Rat).SetString(value)
		if parsed.Sign() <= 0 {
			return fmt.Errorf("must be greater than zero")
		}
	}
	return nil
}

func validateIdentifier(name, value string) error {
	if value == "" || len(value) > 256 || strings.HasPrefix(value, "-") || !identifierPattern.MatchString(value) {
		return invalidCommand(name + " contains unsupported characters or is too long")
	}
	return nil
}
