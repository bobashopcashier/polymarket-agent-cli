package sanitize

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

func TestTextEscapesTerminalControlsAndBidi(t *testing.T) {
	input := "safe\x1b[31m\nleft\u202Eright\u2060word\u2028line"
	got := Text(input)
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\n') || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("unsafe rune survived sanitization: %q", got)
	}
	for _, visible := range []string{`\u001B`, `\u000A`, `\u202E`, `\u2060`, `\u2028`} {
		if !strings.Contains(got, visible) {
			t.Fatalf("missing visible escape %q in %q", visible, got)
		}
	}
}

func TestValueRecursivelySanitizesStringsAndKeys(t *testing.T) {
	value := map[string]any{
		"safe\u202Ekey": []any{"line\nbreak", map[string]any{"zero\u200Bwidth": "\x1b[31mred"}},
		"number":        json.Number("12345678901234567890"),
		"bool":          true,
		"null":          nil,
	}
	got, err := Value(value)
	if err != nil {
		t.Fatal(err)
	}
	object := got.(map[string]any)
	items := object[`safe\u202Ekey`].([]any)
	if items[0] != `line\u000Abreak` {
		t.Fatalf("string was not sanitized: %#v", items[0])
	}
	nested := items[1].(map[string]any)
	if nested[`zero\u200Bwidth`] != `\u001B[31mred` {
		t.Fatalf("nested data was not sanitized: %#v", nested)
	}
	if object["number"].(json.Number).String() != "12345678901234567890" || object["bool"] != true || object["null"] != nil {
		t.Fatalf("JSON primitives changed: %#v", object)
	}
}

func TestValueRejectsSanitizedKeyCollision(t *testing.T) {
	_, err := Value(map[string]any{"line\nbreak": 1, `line\u000Abreak`: 2})
	var appErr *contract.Error
	if !errors.As(err, &appErr) || appErr.Code != "sanitized_key_collision" {
		t.Fatalf("got %v, want sanitized_key_collision", err)
	}
}

func TestTextReplacesInvalidUTF8(t *testing.T) {
	got := Text(string([]byte{'a', 0xff, 'b'}))
	if got != `a\uFFFDb` {
		t.Fatalf("got %q", got)
	}
}

func FuzzTextContainsNoUnsafeRunes(f *testing.F) {
	f.Add("plain")
	f.Add("\x1b[2J\u202E")
	f.Fuzz(func(t *testing.T, value string) {
		for _, current := range Text(value) {
			if IsUnsafeRune(current) {
				t.Fatalf("unsafe rune %U survived", current)
			}
		}
	})
}
