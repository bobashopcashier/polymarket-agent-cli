package upstream

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildLimitOrderProducesFixedArguments(t *testing.T) {
	command, err := BuildLimitOrder(LimitOrder{
		TokenID:  "123456789",
		Side:     SideBuy,
		Price:    "0.45",
		Size:     "20",
		Type:     OrderGTC,
		PostOnly: true,
	})
	if err != nil {
		t.Fatalf("BuildLimitOrder() error = %v", err)
	}
	want := []string{
		"clob", "create-order", "--token", "123456789", "--side", "buy",
		"--price", "0.45", "--size", "20", "--order-type", "GTC", "--post-only",
	}
	if !reflect.DeepEqual(command.args, want) {
		t.Fatalf("args = %#v, want %#v", command.args, want)
	}
	if !command.requiresSecret || command.Operation() != "clob.create_order" {
		t.Fatalf("unexpected command metadata: %#v", command)
	}
}

func TestOrderBuildersRejectUnsafeOrAmbiguousValues(t *testing.T) {
	tests := []struct {
		name  string
		build func() error
	}{
		{
			name: "token flag injection",
			build: func() error {
				_, err := BuildLimitOrder(LimitOrder{TokenID: "--help", Side: SideBuy, Price: "0.5", Size: "1", Type: OrderGTC})
				return err
			},
		},
		{
			name: "scientific decimal",
			build: func() error {
				_, err := BuildLimitOrder(LimitOrder{TokenID: "1", Side: SideBuy, Price: "5e-1", Size: "1", Type: OrderGTC})
				return err
			},
		},
		{
			name: "price above one",
			build: func() error {
				_, err := BuildLimitOrder(LimitOrder{TokenID: "1", Side: SideBuy, Price: "1.01", Size: "1", Type: OrderGTC})
				return err
			},
		},
		{
			name: "post-only fill or kill",
			build: func() error {
				_, err := BuildLimitOrder(LimitOrder{TokenID: "1", Side: SideBuy, Price: "0.5", Size: "1", Type: OrderType("FOK"), PostOnly: true})
				return err
			},
		},
		{
			name: "unsupported GTD without expiration",
			build: func() error {
				_, err := BuildLimitOrder(LimitOrder{TokenID: "1", Side: SideBuy, Price: "0.5", Size: "1", Type: OrderType("GTD"), PostOnly: true})
				return err
			},
		},
		{
			name: "non-post-only limit order",
			build: func() error {
				_, err := BuildLimitOrder(LimitOrder{TokenID: "1", Side: SideBuy, Price: "0.5", Size: "1", Type: OrderGTC})
				return err
			},
		},
		{
			name: "order ID flag injection",
			build: func() error {
				_, err := BuildCancelOrder("--help")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamError *Error
			if err := test.build(); !errors.As(err, &upstreamError) || upstreamError.Kind != ErrorInvalidCommand {
				t.Fatalf("error = %v, want invalid_command", err)
			}
		})
	}
}

func TestCancelAndAuthenticatedReadBuilders(t *testing.T) {
	batch, err := BuildCancelOrders([]string{"order-1", "0xabc"})
	if err != nil {
		t.Fatalf("BuildCancelOrders() error = %v", err)
	}
	if got := strings.Join(batch.args, " "); got != "clob cancel-orders order-1,0xabc" {
		t.Fatalf("batch args = %q", got)
	}

	conditionID := "0x" + strings.Repeat("ab", 32)
	orders, err := BuildOpenOrders(OrdersQuery{MarketConditionID: conditionID, AssetTokenID: "99", Cursor: "MA=="})
	if err != nil {
		t.Fatalf("BuildOpenOrders() error = %v", err)
	}
	want := "clob orders --market " + conditionID + " --asset 99 --cursor MA=="
	if got := strings.Join(orders.args, " "); got != want {
		t.Fatalf("orders args = %q, want %q", got, want)
	}

	balance, err := BuildBalance(BalanceQuery{AssetType: AssetConditional, TokenID: "99"})
	if err != nil {
		t.Fatalf("BuildBalance() error = %v", err)
	}
	if got := strings.Join(balance.args, " "); got != "clob balance --asset-type conditional --token 99" {
		t.Fatalf("balance args = %q", got)
	}
}

func TestApprovalCheckAuthenticationBoundary(t *testing.T) {
	public, err := BuildApprovalCheck("0x" + strings.Repeat("12", 20))
	if err != nil {
		t.Fatalf("BuildApprovalCheck(address) error = %v", err)
	}
	if public.requiresSecret {
		t.Fatal("explicit-address approval check unexpectedly requires a secret")
	}

	account, err := BuildApprovalCheck("")
	if err != nil {
		t.Fatalf("BuildApprovalCheck(empty) error = %v", err)
	}
	if !account.requiresSecret {
		t.Fatal("account-derived approval check must require a secret")
	}
}
