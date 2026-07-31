package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

func TestWriteJSONIsBoundedBeforeWrite(t *testing.T) {
	var output bytes.Buffer
	err := WriteJSON(&output, map[string]string{"payload": strings.Repeat("x", 100)}, true, 32)
	if err == nil {
		t.Fatal("expected output limit error")
	}
	if output.Len() != 0 {
		t.Fatalf("partial oversized output was written: %q", output.String())
	}
	var appErr *contract.Error
	if !errors.As(err, &appErr) || appErr.Code != "output_too_large" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectEnvelopePreservesSafetyMetadata(t *testing.T) {
	sourceCount := 10
	envelope := contract.Success("markets.list", map[string]any{
		"markets": []map[string]any{{"id": "1", "question": "q", "volume": "3"}},
	}, contract.Meta{
		Effects:    contract.Effects{Network: contract.NetworkRead, Mutation: contract.MutationNone},
		Pagination: &contract.Pagination{ItemsEmitted: 1, Complete: false, NextCursor: "next"},
		Truncation: []contract.Truncation{{Path: "data.markets", Reason: "max_items", SourceCount: &sourceCount, EmittedCount: 1}},
	})
	projected, err := ProjectEnvelope(envelope, "markets.id,markets.question", []string{"markets.id", "markets.question", "markets.volume"})
	if err != nil {
		t.Fatal(err)
	}
	data := projected.Data.(map[string]any)
	items := data["markets"].([]any)
	item := items[0].(map[string]any)
	if _, exists := item["volume"]; exists || item["id"] != "1" {
		t.Fatalf("unexpected projection: %#v", projected.Data)
	}
	if projected.Meta.Pagination.NextCursor != "next" || len(projected.Meta.Truncation) != 1 {
		t.Fatalf("safety metadata was lost: %#v", projected.Meta)
	}
}

func TestProjectEmptyCollectionAgainstSchema(t *testing.T) {
	projected, err := ProjectData(map[string]any{"markets": []any{}}, "markets.id", []string{"markets.id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projected.(map[string]any)["markets"].([]any)) != 0 {
		t.Fatalf("unexpected projection: %#v", projected)
	}
	if _, err := ProjectData(map[string]any{"markets": []any{}}, "markets.missing", []string{"markets.id"}); err == nil {
		t.Fatal("expected unknown schema field rejection")
	}
}

func TestWriterSeparatesSuccessAndErrorStreams(t *testing.T) {
	var stdout, stderr bytes.Buffer
	writer := NewWriter(&stdout, &stderr, Options{Format: FormatJSON, Compact: true})
	envelope := contract.Success("clob.book", map[string]any{"id": "1"}, contract.Meta{Effects: contract.Effects{Network: contract.NetworkRead}})
	if err := writer.Success(envelope, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	exit, err := writer.Failure("orders.create", contract.PolicyDenied("policy_denied", "denied"))
	if err != nil {
		t.Fatal(err)
	}
	if exit != contract.ExitPolicy || !strings.Contains(stdout.String(), `"ok":true`) || !strings.Contains(stderr.String(), `"ok":false`) {
		t.Fatalf("unexpected streams, exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestNDJSONRecordsAndTerminalState(t *testing.T) {
	var output bytes.Buffer
	stream := NewNDJSONWriter(&output, "markets.list", 4096)
	if err := stream.Item(map[string]any{"id": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Summary(contract.Pagination{ItemsEmitted: 1, Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := stream.Item(map[string]any{"id": "2"}); err == nil {
		t.Fatal("expected item-after-summary rejection")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d records: %q", len(lines), output.String())
	}
	for _, line := range lines {
		var record contract.NDJSONRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid NDJSON record: %v", err)
		}
	}
}

func TestNDJSONCapDoesNotWritePartialRecord(t *testing.T) {
	var output bytes.Buffer
	stream := NewNDJSONWriter(&output, "markets.list", 64)
	if err := stream.Item(map[string]any{"payload": strings.Repeat("x", 100)}); err == nil {
		t.Fatal("expected NDJSON cap error")
	}
	if output.Len() != 0 {
		t.Fatalf("wrote partial NDJSON: %q", output.String())
	}
}

func FuzzParseFieldMask(f *testing.F) {
	f.Add("markets.id,markets.question")
	f.Add("a..b")
	f.Fuzz(func(t *testing.T, mask string) {
		_, _ = ParseFieldMask(mask)
	})
}
