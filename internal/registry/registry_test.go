package registry

import (
	"reflect"
	"testing"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

func readCommand(id string) CommandSpec {
	path := []string{"markets", "list"}
	if id != "markets.list" {
		path = []string{"clob", "book"}
	}
	return CommandSpec{
		ID: id, Path: path, Summary: "Read data",
		Params:   ObjectSpec{Fields: []FieldSpec{{Name: "tokenId", Kind: KindString, MaxBytes: 128}}},
		Response: ValueSpec{Kind: KindObject},
		Effects:  EffectSpec{Effects: contract.Effects{Network: contract.NetworkRead, Mutation: contract.MutationNone}},
		Output:   OutputSpec{ResponseFields: []string{"tokenId"}},
	}
}

func TestRegistryIsDeterministicAndAppliesDefaults(t *testing.T) {
	r, err := New("1.2.3", readCommand("markets.list"), readCommand("clob.book"))
	if err != nil {
		t.Fatal(err)
	}
	commands := r.Commands()
	if got := []string{commands[0].ID, commands[1].ID}; !reflect.DeepEqual(got, []string{"clob.book", "markets.list"}) {
		t.Fatalf("commands not sorted: %v", got)
	}
	if commands[0].Params.MaximumBytes != defaultMaximumBytes || commands[0].Output.MaximumEncodedOutputBytes == 0 {
		t.Fatalf("defaults were not persisted: %#v", commands[0])
	}
	if command, ok := r.Lookup("CLOB.BOOK"); !ok || command.ID != "clob.book" {
		t.Fatalf("dotted lookup failed: %#v, %v", command, ok)
	}
	if schema, ok := r.Schema("clob", "book"); !ok || schema.SchemaVersion != CommandSchemaVersion {
		t.Fatalf("schema lookup failed: %#v, %v", schema, ok)
	}
	index := r.Index("clob")
	if len(index.Commands) != 1 || index.Commands[0].ID != "clob.book" {
		t.Fatalf("unexpected index: %#v", index)
	}
	wantControls := []string{"--compact", "--fields", "--json", "--output", "--params", "--timeout"}
	gotControls := make([]string, len(index.InvocationControls))
	for index, control := range index.InvocationControls {
		gotControls[index] = control.Name
	}
	if !reflect.DeepEqual(gotControls, wantControls) {
		t.Fatalf("unexpected invocation controls: %v", gotControls)
	}
	schema, _ := r.Schema("clob.book")
	if len(schema.InvocationControls) != len(wantControls) {
		t.Fatalf("schema omitted invocation controls: %#v", schema)
	}
}

func TestRegistryReturnsCopies(t *testing.T) {
	r := MustNew("1", readCommand("clob.book"))
	first, _ := r.Lookup("clob", "book")
	first.Path[0] = "changed"
	first.Params.Fields[0].Name = "changed"
	second, _ := r.Lookup("clob", "book")
	if second.Path[0] != "clob" || second.Params.Fields[0].Name != "tokenId" {
		t.Fatalf("registry leaked mutable storage: %#v", second)
	}
}

func TestRegistryRejectsDuplicateAndUnsafeMutation(t *testing.T) {
	command := readCommand("clob.book")
	if _, err := New("1", command, command); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	command.ID = "orders.create"
	command.Path = []string{"orders", "create"}
	command.Effects.Effects = contract.Effects{Network: contract.NetworkWrite, Mutation: contract.MutationOrderCreate, Financial: true}
	if _, err := New("1", command); err == nil {
		t.Fatal("expected mutation without dry-run to be rejected")
	}
	command.Effects.DryRun = true
	command.Effects.Confirmation = ConfirmationPlanHash
	if _, err := New("1", command); err != nil {
		t.Fatalf("safe mutation rejected: %v", err)
	}
}

func TestRegistryRejectsSchemaDriftHazards(t *testing.T) {
	command := readCommand("clob.book")
	command.Params.Fields = append(command.Params.Fields, FieldSpec{Name: "tokenId", Kind: KindString})
	if _, err := New("1", command); err == nil {
		t.Fatal("expected duplicate field rejection")
	}
	command = readCommand("clob.book")
	command.Params.Fields[0].Pattern = "["
	if _, err := New("1", command); err == nil {
		t.Fatal("expected invalid pattern rejection")
	}
}
