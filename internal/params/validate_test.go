package params

import "testing"

func TestValidationHelpers(t *testing.T) {
	if err := RejectControls("query", "safe text"); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"line\nbreak", "\x1b[31mred", "left\u202Eright"} {
		if err := RejectControls("query", unsafe); err == nil {
			t.Fatalf("expected unsafe text rejection for %q", unsafe)
		}
	}
	for _, unsafe := range []string{"abc?x=1", "abc#frag", "%2e%2e", "../secret", "a/b"} {
		if err := ValidateResourceID("id", unsafe, 128); err == nil {
			t.Fatalf("expected unsafe ID rejection for %q", unsafe)
		}
	}
	if err := ValidateNumericID("token", "123456"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHex32("condition", "0x"+string(make([]byte, 64))); err == nil {
		t.Fatal("NUL bytes must not validate as hexadecimal")
	}
	if err := ValidateDecimal("price", "0.50", "0", "1"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"1e-3", "+1", ".5", "1/2", "NaN"} {
		if err := ValidateDecimal("price", invalid, "0", "1"); err == nil {
			t.Fatalf("expected decimal rejection for %q", invalid)
		}
	}
}

func TestArgumentValidationRejectsCredentialFlagsWithoutInspectingValues(t *testing.T) {
	if err := RejectArgumentControls([]string{"markets", "list", "--params", `{"q":"privateKey is ordinary text here"}`}); err != nil {
		t.Fatalf("safe arguments rejected: %v", err)
	}
	for _, arguments := range [][]string{
		{"--private-key", "do-not-reflect"},
		{"--private_key=do-not-reflect"},
		{"--apiKey", "do-not-reflect"},
		{"--seed-phrase", "do-not-reflect"},
	} {
		if err := RejectArgumentControls(arguments); err == nil {
			t.Fatalf("credential arguments accepted: %#v", arguments)
		}
	}
}

func FuzzRejectControls(f *testing.F) {
	f.Add("plain")
	f.Add("\x1b[2J")
	f.Fuzz(func(t *testing.T, input string) {
		_ = RejectControls("input", input)
	})
}
