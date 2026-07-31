package console

import (
	"strings"
	"testing"
)

func TestReadBoundedLine(t *testing.T) {
	line, err := readBoundedLine(strings.NewReader("authorize\n"), 32)
	if err != nil || line != "authorize\n" {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if _, err := readBoundedLine(strings.NewReader(strings.Repeat("x", 33)), 32); err == nil {
		t.Fatal("expected oversized input to fail")
	}
}

func TestZero(t *testing.T) {
	secret := []byte("secret")
	zero(secret)
	for _, value := range secret {
		if value != 0 {
			t.Fatal("secret bytes were not cleared")
		}
	}
}
