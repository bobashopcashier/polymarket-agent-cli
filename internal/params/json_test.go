package params

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
)

func requestSchema() registry.ObjectSpec {
	minimum := "1"
	maximum := "100"
	return registry.ObjectSpec{
		MaximumBytes: MaximumBytes,
		Fields: []registry.FieldSpec{
			{Name: "tokenId", Kind: registry.KindString, Required: true, MaxBytes: 128, Pattern: `^[0-9]+$`},
			{Name: "side", Kind: registry.KindString, Required: true, Enum: []string{"buy", "sell"}, Normalize: registry.NormalizeLowercase},
			{Name: "limit", Kind: registry.KindInteger, Default: 25, Minimum: &minimum, Maximum: &maximum},
			{Name: "filters", Kind: registry.KindObject, Properties: []registry.FieldSpec{{Name: "active", Kind: registry.KindBoolean}}},
		},
	}
}

func TestDecodeIntoStrictNormalizedObject(t *testing.T) {
	type filters struct {
		Active bool `json:"active"`
	}
	type request struct {
		TokenID string  `json:"tokenId"`
		Side    string  `json:"side"`
		Limit   int     `json:"limit"`
		Filters filters `json:"filters"`
	}
	var got request
	err := DecodeInto([]byte(`{"tokenId":"123","side":" BUY ","filters":{"active":true}}`), requestSchema(), &got)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != "123" || got.Side != "buy" || got.Limit != 25 || !got.Filters.Active {
		t.Fatalf("unexpected request: %#v", got)
	}
}

func TestDecodeRejectsDuplicateUnknownMissingAndCredentialKeys(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		code string
	}{
		{"duplicate top", `{"tokenId":"1","tokenId":"2","side":"buy"}`, "duplicate_parameter"},
		{"duplicate nested", `{"tokenId":"1","side":"buy","filters":{"active":true,"active":false}}`, "duplicate_parameter"},
		{"unknown", `{"tokenId":"1","side":"buy","surprise":true}`, "unknown_parameter"},
		{"missing", `{"side":"buy"}`, "missing_parameter"},
		{"wrong type", `{"tokenId":1,"side":"buy"}`, "invalid_parameter_type"},
		{"private key", `{"tokenId":"1","side":"buy","privateKey":"secret"}`, "credential_parameter"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.raw), requestSchema())
			var appErr *contract.Error
			if !errors.As(err, &appErr) || appErr.Code != test.code {
				t.Fatalf("got %v, want contract code %q", err, test.code)
			}
		})
	}
}

func TestDecodeRejectsTrailingAndOversize(t *testing.T) {
	if _, err := DecodeObject([]byte(`{} {}`), MaximumBytes); err == nil {
		t.Fatal("expected trailing object rejection")
	}
	if _, err := ReadSource("-", strings.NewReader(strings.Repeat("x", 11)), 10); err == nil {
		t.Fatal("expected bounded stdin rejection")
	}
	if _, err := ReadSource(`{}`, nil, MaximumBytes); err != nil {
		t.Fatalf("inline source failed: %v", err)
	}
}

func TestArrayItemValidation(t *testing.T) {
	schema := registry.ObjectSpec{Fields: []registry.FieldSpec{{
		Name: "ids", Kind: registry.KindArray, MaxItems: 2,
		Items: &registry.ValueSpec{Kind: registry.KindString, Pattern: `^[0-9]+$`},
	}}}
	if _, err := Decode([]byte(`{"ids":["1","2"]}`), schema); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte(`{"ids":["1","bad"]}`), schema); err == nil {
		t.Fatal("expected invalid array item rejection")
	}
	if _, err := Decode([]byte(`{"ids":["1","2","3"]}`), schema); err == nil {
		t.Fatal("expected array size rejection")
	}
}

func TestNumbersRemainExact(t *testing.T) {
	schema := registry.ObjectSpec{Fields: []registry.FieldSpec{{Name: "value", Kind: registry.KindInteger}}}
	result, err := Decode([]byte(`{"value":123456789012345678901234567890}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := result["value"].(json.Number).String(); got != "123456789012345678901234567890" {
		t.Fatalf("number lost precision: %s", got)
	}
}

func FuzzDecodeObjectNeverPanics(f *testing.F) {
	f.Add([]byte(`{"tokenId":"1"}`))
	f.Add([]byte(`{"a":1,"a":2}`))
	f.Add([]byte{0xff, '{', '}'})
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = DecodeObject(input, MaximumBytes)
	})
}
