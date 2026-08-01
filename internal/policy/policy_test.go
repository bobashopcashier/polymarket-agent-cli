package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

func orderInvocation() Invocation {
	return Invocation{
		Command: "orders.create",
		Effects: contract.Effects{
			Network: contract.NetworkWrite, Mutation: contract.MutationOrderCreate,
			Financial: true, Signing: true, Broadcast: true, Risk: contract.RiskHigh,
		},
		Wallet: "0xabc", ConditionID: "0xmarket", OrderKind: "limit",
		OrderNotionalUSDC: "5.00", OpenNotionalUSDC: "8.00", BatchSize: 1,
		PostOnly: true, ClientRequestID: "request-1",
	}
}

func allowPolicy() Policy {
	return Policy{
		SchemaVersion: SchemaVersion, Profile: "production", AllowMutations: true,
		AllowedWallets: []string{"0xAbC"}, AllowedConditionIDs: []string{"0xMarket"},
		MaxOrderNotionalUSDC: "10.00", MaxOpenNotionalUSDC: "20.00",
		MaxBatchSize: 2, MaxSlippageBPS: 50, RequirePostOnly: true,
	}
}

func TestDefaultPolicyDeniesMutationButAllowsRead(t *testing.T) {
	policy := Default("production")
	mutation, err := Evaluate(policy, orderInvocation())
	if err != nil {
		t.Fatal(err)
	}
	if mutation.Allowed || mutation.Error() == nil {
		t.Fatalf("default policy allowed mutation: %#v", mutation)
	}
	read, err := Evaluate(policy, Invocation{Command: "markets.list", Effects: contract.Effects{Network: contract.NetworkRead, Mutation: contract.MutationNone}})
	if err != nil || !read.Allowed {
		t.Fatalf("default policy denied read: %#v, %v", read, err)
	}
}

func TestPolicyAllowsBoundedLimitOrder(t *testing.T) {
	decision, err := Evaluate(allowPolicy(), orderInvocation())
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.Error() != nil || decision.PolicyDigest == "" {
		t.Fatalf("unexpected denial: %#v", decision)
	}
}

func TestPolicyReportsAllRelevantViolations(t *testing.T) {
	invocation := orderInvocation()
	invocation.OrderKind = "market"
	invocation.OrderNotionalUSDC = "25"
	invocation.PostOnly = false
	invocation.ClientRequestID = ""
	decision, err := Evaluate(allowPolicy(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || len(decision.Violations) < 4 {
		t.Fatalf("expected multiple violations: %#v", decision)
	}
	var appErr *contract.Error
	if !errors.As(decision.Error(), &appErr) || appErr.ExitCode != contract.ExitPolicy {
		t.Fatalf("unexpected decision error: %v", decision.Error())
	}
}

func TestPolicyRejectsNonPlainDecimals(t *testing.T) {
	for _, invalid := range []string{"1/2", "1e3", "+1", "-1", ".5"} {
		policy := allowPolicy()
		policy.MaxOrderNotionalUSDC = invalid
		if err := policy.Validate(); err == nil {
			t.Fatalf("expected invalid decimal rejection for %q", invalid)
		}
	}
}

func TestPlanConfirmationBindsInputsAndExpires(t *testing.T) {
	policy := allowPolicy()
	decision, err := Evaluate(policy, orderInvocation())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	input := PlanInput{
		Command: "orders.create", Params: map[string]any{"price": "0.50", "size": "10"},
		Profile: "production", Wallet: "0xabc", ChainID: 137,
		Effects: orderInvocation().Effects, PolicyDecision: decision, ClientRequestID: "request-1",
	}
	result, err := DryRun(now, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Executes || result.Plan.Effects.Executed {
		t.Fatalf("dry-run claimed execution: %#v", result)
	}
	if err := Verify(result.Plan, result.Plan.Confirmation, now.Add(time.Minute)); err != nil {
		t.Fatalf("valid confirmation rejected: %v", err)
	}
	tampered := result.Plan
	tampered.Params = []byte(`{"price":"0.90","size":"10"}`)
	if err := Verify(tampered, result.Plan.Confirmation, now.Add(time.Minute)); err == nil {
		t.Fatal("tampered plan was accepted")
	}
	if err := Verify(result.Plan, result.Plan.Confirmation, result.Plan.ExpiresAt); err == nil {
		t.Fatal("expired plan was accepted")
	}
}

func TestDeniedDecisionCannotBuildPlan(t *testing.T) {
	decision, err := Evaluate(Default("production"), orderInvocation())
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildPlan(time.Now(), PlanInput{Command: "orders.create", Profile: "production", PolicyDecision: decision})
	if err == nil {
		t.Fatal("denied decision produced a plan")
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	f.Add("sha256:" + "00")
	f.Add("not-a-hash")
	f.Fuzz(func(t *testing.T, confirmation string) {
		_ = Verify(Plan{SchemaVersion: PlanSchemaVersion, ExpiresAt: time.Now().Add(time.Minute)}, confirmation, time.Now())
	})
}
