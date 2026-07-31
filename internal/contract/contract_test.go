package contract

import (
	"errors"
	"testing"
)

func TestEnvelopeVersionsAndExitTaxonomy(t *testing.T) {
	envelope := Success("markets.list", map[string]any{"count": 1}, Meta{Effects: Effects{Network: NetworkRead, Mutation: MutationNone}})
	if envelope.SchemaVersion != SuccessSchemaVersion || !envelope.OK || envelope.Command != "markets.list" {
		t.Fatalf("unexpected success envelope: %#v", envelope)
	}

	appErr := PolicyDenied("policy_denied", "denied")
	failure := Failure("orders.create", appErr)
	if failure.SchemaVersion != ErrorSchemaVersion || failure.OK || failure.Error.ExitCode != ExitPolicy {
		t.Fatalf("unexpected error envelope: %#v", failure)
	}
	if ExitIndeterminate.Int() == ExitTransient.Int() {
		t.Fatal("indeterminate and transient outcomes must have distinct exits")
	}
}

func TestAsErrorHidesArbitraryCauseMessage(t *testing.T) {
	err := AsError(errors.New("secret upstream body"))
	if err.Code != "internal_error" || err.Message == "secret upstream body" || err.ExitCode != ExitInternal {
		t.Fatalf("unsafe arbitrary error conversion: %#v", err)
	}
}

func TestAsErrorRepairsZeroExit(t *testing.T) {
	bad := NewError("broken", CategoryInternal, "broken", ExitOK)
	got := AsError(bad)
	if got.ExitCode != ExitInternal {
		t.Fatalf("got exit %d, want %d", got.ExitCode, ExitInternal)
	}
	if bad.ExitCode != ExitOK {
		t.Fatal("AsError mutated caller-owned error")
	}
}
