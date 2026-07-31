package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeyringStoreRoundTripAndNotFound(t *testing.T) {
	keyring.MockInit()
	store, err := NewKeyringStore("pmx-wallet-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	secret := []byte{0, 1, 2, 3, 0xff}
	if err := store.Set(ctx, "wallet/alpha", secret); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "wallet/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(secret) {
		t.Fatalf("got %v, want %v", got, secret)
	}
	zero(got)
	if err := store.Delete(ctx, "wallet/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "wallet/alpha"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing key returned %v", err)
	}
	if err := store.Delete(ctx, "wallet/alpha"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("second delete returned %v", err)
	}
}

func TestKeyringStoreRejectsUnsafeIdentifiersAndCanceledContext(t *testing.T) {
	keyring.MockInit()
	store, _ := NewKeyringStore("pmx-wallet-test")
	if err := store.Set(context.Background(), "../secret", []byte("value")); err == nil {
		t.Fatal("unsafe keyring ID was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Set(ctx, "wallet/alpha", []byte("value")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context returned %v", err)
	}
}
