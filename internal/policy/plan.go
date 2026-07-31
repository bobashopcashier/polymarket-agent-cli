package policy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

const (
	PlanSchemaVersion   = "pmx.plan/v1"
	DryRunSchemaVersion = "pmx.dry-run/v1"
	defaultPlanTTL      = 5 * time.Minute
	maximumPlanTTL      = 30 * time.Minute
)

type PlanInput struct {
	Command         string
	Params          any
	Profile         string
	Wallet          string
	ChainID         int64
	Effects         contract.Effects
	PolicyDecision  Decision
	ClientRequestID string
	TTL             time.Duration
}

type Plan struct {
	SchemaVersion   string           `json:"schemaVersion"`
	Command         string           `json:"command"`
	Params          json.RawMessage  `json:"params"`
	Profile         string           `json:"profile"`
	Wallet          string           `json:"wallet,omitempty"`
	ChainID         int64            `json:"chainId"`
	Effects         contract.Effects `json:"effects"`
	PolicyDigest    string           `json:"policyDigest"`
	ClientRequestID string           `json:"clientRequestId,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	ExpiresAt       time.Time        `json:"expiresAt"`
	Confirmation    string           `json:"confirmation"`
}

type DryRunResult struct {
	SchemaVersion string   `json:"schemaVersion"`
	Executes      bool     `json:"executes"`
	Plan          Plan     `json:"plan"`
	Decision      Decision `json:"decision"`
}

func BuildPlan(now time.Time, input PlanInput) (Plan, error) {
	if input.Command == "" || input.Profile == "" {
		return Plan{}, contract.Invalid("invalid_plan", "command and profile are required to build a plan")
	}
	if err := input.PolicyDecision.Error(); err != nil {
		return Plan{}, err
	}
	if !input.Effects.IsMutation() {
		return Plan{}, contract.Invalid("invalid_plan", "dry-run plans are required only for mutation commands")
	}
	if input.Effects.Signing && input.Wallet == "" {
		return Plan{}, contract.Invalid("invalid_plan", "signing plans require a wallet")
	}
	if input.Effects.Broadcast && input.ChainID <= 0 {
		return Plan{}, contract.Invalid("invalid_plan", "broadcast plans require a positive chain ID")
	}
	ttl := input.TTL
	if ttl == 0 {
		ttl = defaultPlanTTL
	}
	if ttl < time.Second || ttl > maximumPlanTTL {
		return Plan{}, contract.Invalid("invalid_plan", "plan TTL must be between one second and thirty minutes")
	}
	parameters, err := json.Marshal(input.Params)
	if err != nil {
		return Plan{}, contract.Invalid("invalid_plan", "could not encode normalized plan parameters").WithCause(err)
	}
	if len(parameters) > 64<<10 {
		return Plan{}, contract.Invalid("invalid_plan", "normalized plan parameters exceed 65536 bytes")
	}
	now = now.UTC().Round(0)
	input.Effects.Executed = false
	plan := Plan{
		SchemaVersion: PlanSchemaVersion, Command: input.Command, Params: parameters,
		Profile: input.Profile, Wallet: input.Wallet, ChainID: input.ChainID,
		Effects: input.Effects, PolicyDigest: input.PolicyDecision.PolicyDigest,
		ClientRequestID: input.ClientRequestID, CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	confirmation, err := confirmationFor(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Confirmation = confirmation
	return plan, nil
}

func DryRun(now time.Time, input PlanInput) (DryRunResult, error) {
	plan, err := BuildPlan(now, input)
	if err != nil {
		return DryRunResult{}, err
	}
	return DryRunResult{
		SchemaVersion: DryRunSchemaVersion, Executes: false, Plan: plan, Decision: input.PolicyDecision,
	}, nil
}

func Verify(plan Plan, confirmation string, now time.Time) error {
	if plan.SchemaVersion != PlanSchemaVersion {
		return contract.PolicyDenied("invalid_confirmation", "plan schema version is not supported")
	}
	if !now.UTC().Before(plan.ExpiresAt.UTC()) {
		return contract.PolicyDenied("expired_confirmation", "the dry-run plan has expired")
	}
	expected, err := confirmationFor(plan)
	if err != nil {
		return err
	}
	providedBytes, providedErr := decodeConfirmation(confirmation)
	expectedBytes, expectedErr := decodeConfirmation(expected)
	if providedErr != nil || expectedErr != nil || len(providedBytes) != len(expectedBytes) || subtle.ConstantTimeCompare(providedBytes, expectedBytes) != 1 {
		return contract.PolicyDenied("invalid_confirmation", "confirmation does not match the dry-run plan")
	}
	return nil
}

func confirmationFor(plan Plan) (string, error) {
	copy := plan
	copy.Confirmation = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", contract.Internal("could not encode confirmation plan", err)
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func decodeConfirmation(value string) ([]byte, error) {
	const prefix = "sha256:"
	if len(value) != len(prefix)+sha256.Size*2 || value[:len(prefix)] != prefix {
		return nil, fmt.Errorf("invalid confirmation format")
	}
	return hex.DecodeString(value[len(prefix):])
}
