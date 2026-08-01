package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

const SchemaVersion = "pmx.policy/v1"

const DecisionSchemaVersion = "pmx.policy-decision/v1"

var plainDecimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

// Policy is deny-by-default for every mutation. Decimal limits are strings and
// are compared exactly.
type Policy struct {
	SchemaVersion          string   `json:"schemaVersion"`
	Profile                string   `json:"profile"`
	AllowMutations         bool     `json:"allowMutations"`
	AllowedWallets         []string `json:"allowedWallets,omitempty"`
	AllowedConditionIDs    []string `json:"allowedConditionIds,omitempty"`
	MaxOrderNotionalUSDC   string   `json:"maxOrderNotionalUsdc,omitempty"`
	MaxOpenNotionalUSDC    string   `json:"maxOpenNotionalUsdc,omitempty"`
	MaxBatchSize           int      `json:"maxBatchSize,omitempty"`
	MaxSlippageBPS         int      `json:"maxSlippageBps,omitempty"`
	RequirePostOnly        bool     `json:"requirePostOnly"`
	AllowMarketOrders      bool     `json:"allowMarketOrders"`
	AllowCancelAll         bool     `json:"allowCancelAll"`
	AllowUnlimitedApproval bool     `json:"allowUnlimitedApproval"`
}

func Default(profile string) Policy {
	return Policy{SchemaVersion: SchemaVersion, Profile: profile}
}

func (p Policy) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return contract.Invalid("invalid_policy", fmt.Sprintf("policy schemaVersion must be %q", SchemaVersion))
	}
	if strings.TrimSpace(p.Profile) == "" {
		return contract.Invalid("invalid_policy", "policy profile is required")
	}
	for name, value := range map[string]string{
		"maxOrderNotionalUsdc": p.MaxOrderNotionalUSDC,
		"maxOpenNotionalUsdc":  p.MaxOpenNotionalUSDC,
	} {
		if value == "" {
			continue
		}
		if err := validateNonnegativeDecimal(name, value); err != nil {
			return err
		}
	}
	if p.MaxBatchSize < 0 || p.MaxSlippageBPS < 0 || p.MaxSlippageBPS > 10_000 {
		return contract.Invalid("invalid_policy", "policy numeric limits are outside their valid range")
	}
	return nil
}

func (p Policy) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return "", contract.Internal("could not encode policy", err)
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

type Invocation struct {
	Command           string
	Effects           contract.Effects
	Wallet            string
	ConditionID       string
	OrderKind         string
	OrderNotionalUSDC string
	OpenNotionalUSDC  string
	BatchSize         int
	SlippageBPS       int
	PostOnly          bool
	UnlimitedApproval bool
	ClientRequestID   string
}

type Violation struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Decision struct {
	SchemaVersion string      `json:"schemaVersion"`
	Allowed       bool        `json:"allowed"`
	PolicyDigest  string      `json:"policyDigest"`
	Violations    []Violation `json:"violations,omitempty"`
}

func Evaluate(p Policy, invocation Invocation) (Decision, error) {
	digest, err := p.Digest()
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{SchemaVersion: DecisionSchemaVersion, Allowed: true, PolicyDigest: digest}
	deny := func(code, message string, details map[string]any) {
		decision.Violations = append(decision.Violations, Violation{Code: code, Message: message, Details: details})
		decision.Allowed = false
	}
	if !invocation.Effects.IsMutation() {
		return decision, nil
	}
	if !p.AllowMutations {
		deny("mutations_disabled", "the active policy denies all mutations", nil)
	}
	if invocation.Effects.Signing && len(p.AllowedWallets) == 0 {
		deny("wallet_allowlist_required", "signing operations require an explicit wallet allowlist", nil)
	}
	if len(p.AllowedWallets) > 0 && !containsFold(p.AllowedWallets, invocation.Wallet) {
		deny("wallet_not_allowed", "the signing wallet is not allowed by policy", map[string]any{"wallet": invocation.Wallet})
	}
	if len(p.AllowedConditionIDs) > 0 && !containsFold(p.AllowedConditionIDs, invocation.ConditionID) {
		deny("market_not_allowed", "the market condition is not allowed by policy", map[string]any{"conditionId": invocation.ConditionID})
	}
	if invocation.BatchSize < 0 {
		return Decision{}, contract.Invalid("invalid_request", "batch size cannot be negative")
	}
	if p.MaxBatchSize > 0 && invocation.BatchSize > p.MaxBatchSize {
		deny("batch_limit_exceeded", "the request exceeds the policy batch limit", map[string]any{"requested": invocation.BatchSize, "maximum": p.MaxBatchSize})
	}
	if invocation.SlippageBPS < 0 || invocation.SlippageBPS > 10_000 {
		return Decision{}, contract.Invalid("invalid_request", "slippage must be between 0 and 10000 basis points")
	}
	if p.MaxSlippageBPS > 0 && invocation.SlippageBPS > p.MaxSlippageBPS {
		deny("slippage_limit_exceeded", "the request exceeds the policy slippage limit", map[string]any{"requestedBps": invocation.SlippageBPS, "maximumBps": p.MaxSlippageBPS})
	}
	if p.RequirePostOnly && invocation.Effects.Mutation == contract.MutationOrderCreate && !invocation.PostOnly {
		deny("post_only_required", "the active policy requires post-only limit orders", nil)
	}
	if strings.EqualFold(invocation.OrderKind, "market") && !p.AllowMarketOrders {
		deny("market_orders_disabled", "the active policy denies market orders", nil)
	}
	if invocation.Effects.Mutation == contract.MutationCancelAll && !p.AllowCancelAll {
		deny("cancel_all_disabled", "the active policy denies cancel-all operations", nil)
	}
	if invocation.UnlimitedApproval && !p.AllowUnlimitedApproval {
		deny("unlimited_approval_disabled", "the active policy denies unlimited token approvals", nil)
	}
	if invocation.Effects.Financial && strings.TrimSpace(invocation.ClientRequestID) == "" {
		deny("client_request_id_required", "financial mutations require a client request ID", nil)
	}
	if invocation.Effects.Mutation == contract.MutationOrderCreate && invocation.OrderNotionalUSDC == "" {
		return Decision{}, contract.Invalid("invalid_request", "order-create policy evaluation requires order notional")
	}
	if invocation.Effects.Mutation == contract.MutationOrderCreate && p.MaxOrderNotionalUSDC == "" {
		deny("order_limit_required", "order creation requires an explicit maximum notional policy", nil)
	}
	if strings.EqualFold(invocation.OrderKind, "market") && p.MaxSlippageBPS == 0 {
		deny("slippage_limit_required", "market orders require an explicit slippage limit", nil)
	}
	if p.MaxOpenNotionalUSDC != "" && invocation.OpenNotionalUSDC == "" {
		return Decision{}, contract.Invalid("invalid_request", "open-notional policy evaluation requires current open notional")
	}
	if err := compareLimit("order_notional_limit_exceeded", "order notional", invocation.OrderNotionalUSDC, p.MaxOrderNotionalUSDC, deny); err != nil {
		return Decision{}, err
	}
	if err := compareLimit("open_notional_limit_exceeded", "open notional", invocation.OpenNotionalUSDC, p.MaxOpenNotionalUSDC, deny); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (d Decision) Error() error {
	if d.SchemaVersion != DecisionSchemaVersion || d.PolicyDigest == "" {
		return contract.Invalid("invalid_policy_decision", "policy decision is incomplete or has an unsupported schema version")
	}
	if d.Allowed {
		return nil
	}
	details := make([]map[string]any, 0, len(d.Violations))
	for _, violation := range d.Violations {
		details = append(details, map[string]any{
			"code": violation.Code, "message": violation.Message, "details": violation.Details,
		})
	}
	return contract.PolicyDenied("policy_denied", "the active policy denied this operation").WithDetails(map[string]any{
		"policyDigest": d.PolicyDigest, "violations": details,
	})
}

func compareLimit(code, label, requested, maximum string, deny func(string, string, map[string]any)) error {
	if requested == "" {
		return nil
	}
	if err := validateNonnegativeDecimal(label, requested); err != nil {
		return err
	}
	if maximum == "" {
		return nil
	}
	requestNumber, _ := new(big.Rat).SetString(requested)
	maximumNumber, _ := new(big.Rat).SetString(maximum)
	if requestNumber.Cmp(maximumNumber) > 0 {
		deny(code, fmt.Sprintf("the request exceeds the policy %s limit", label), map[string]any{"requested": requested, "maximum": maximum})
	}
	return nil
}

func validateNonnegativeDecimal(label, value string) error {
	if len(value) > 128 || !plainDecimalPattern.MatchString(value) {
		return contract.Invalid("invalid_policy", fmt.Sprintf("%s must be a nonnegative plain decimal string", label))
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok || number.Sign() < 0 {
		return contract.Invalid("invalid_policy", fmt.Sprintf("%s must be a nonnegative plain decimal string", label))
	}
	return nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
