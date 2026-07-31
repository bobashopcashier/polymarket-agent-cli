package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type memorySecrets struct {
	mu        sync.Mutex
	values    map[string][]byte
	setErr    error
	getErr    error
	deleteErr error
}

func newMemorySecrets() *memorySecrets {
	return &memorySecrets{values: make(map[string][]byte)}
}

func (s *memorySecrets) Set(_ context.Context, id string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.values[id] = append([]byte(nil), value...)
	return nil
}

func (s *memorySecrets) Get(_ context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	value, exists := s.values[id]
	if !exists {
		return nil, ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memorySecrets) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, exists := s.values[id]; !exists {
		return ErrSecretNotFound
	}
	delete(s.values, id)
	return nil
}

func (s *memorySecrets) secret(id string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.values[id]...)
}

func testManager(t *testing.T) (*Manager, *memorySecrets, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wallets.json")
	secrets := newMemorySecrets()
	manager, err := NewManager(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return manager, secrets, path
}

func TestGenerateListUseSignAndRemove(t *testing.T) {
	manager, secrets, path := testManager(t)
	ctx := context.Background()
	first, err := manager.Generate(ctx, GenerateOptions{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Generate(ctx, GenerateOptions{Name: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SignatureType != SignatureEOA || first.Address == "" || second.Address == first.Address {
		t.Fatalf("unexpected generated wallets: %#v %#v", first, second)
	}
	active, err := manager.Active(ctx)
	if err != nil || active.Name != "alpha" {
		t.Fatalf("unexpected initial active wallet: %#v, %v", active, err)
	}
	if _, err := manager.Use(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	active, _ = manager.Active(ctx)
	if active.Name != "beta" {
		t.Fatalf("active wallet not changed: %#v", active)
	}
	wallets, err := manager.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if names := []string{wallets[0].Name, wallets[1].Name}; !sort.StringsAreSorted(names) {
		t.Fatalf("wallets are not sorted: %v", names)
	}
	signer, err := manager.GetSigner(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	digest := ethcrypto.Keccak256([]byte("wallet test"))
	signature, err := signer.SignDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ethcrypto.SigToPub(digest, signature)
	if err != nil {
		t.Fatal(err)
	}
	if recovered := ethcrypto.PubkeyToAddress(*publicKey).Hex(); recovered != first.Address {
		t.Fatalf("signature recovered %s, want %s", recovered, first.Address)
	}
	if encoded, err := json.Marshal(signer); err == nil || len(encoded) != 0 {
		t.Fatalf("signer unexpectedly serialized: %q, %v", encoded, err)
	}
	signer.Destroy()
	if _, err := signer.SignDigest(digest); !errors.Is(err, ErrSignerDestroyed) {
		t.Fatalf("destroyed signer returned %v", err)
	}
	storedSecret := secrets.secret(secretID("alpha"))
	metadata, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadata, storedSecret) || strings.Contains(string(metadata), hex.EncodeToString(storedSecret)) {
		t.Fatal("private key appeared in public metadata")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("metadata mode is %o, want 600", info.Mode().Perm())
	}
	zero(storedSecret)
	if err := manager.Remove(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Show(ctx, "beta"); !errors.Is(err, ErrWalletNotFound) {
		t.Fatalf("removed wallet returned %v", err)
	}
	if _, err := manager.Active(ctx); !errors.Is(err, ErrNoActiveWallet) {
		t.Fatalf("removing active wallet returned active state: %v", err)
	}
}

func TestImportRawAndHexWithExpectedAddress(t *testing.T) {
	manager, _, _ := testManager(t)
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := ethcrypto.FromECDSA(key)
	defer zero(secret)
	address := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
	imported, err := manager.Import(context.Background(), ImportOptions{
		Name: "raw", ExpectedAddress: address, SignatureType: SignatureEOA,
	}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Address != address {
		t.Fatalf("got address %s, want %s", imported.Address, address)
	}
	hexInput := []byte("0x" + hex.EncodeToString(secret))
	if _, err := manager.Import(context.Background(), ImportOptions{
		Name: "hex", ExpectedAddress: address, SignatureType: SignatureProxy,
		Funder: "0x0000000000000000000000000000000000000001",
	}, hexInput); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hexInput, []byte("0x"+hex.EncodeToString(secret))) {
		t.Fatal("Import mutated caller-owned secret input")
	}
}

func TestWithSecretScopesAndZeroesCallbackBytes(t *testing.T) {
	manager, _, _ := testManager(t)
	wallet, err := manager.Generate(context.Background(), GenerateOptions{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	var observed []byte
	err = manager.WithSecret(context.Background(), "alpha", func(public PublicWallet, secret []byte) error {
		if public != wallet || len(secret) != 32 {
			t.Fatalf("unexpected callback values: %#v, %d", public, len(secret))
		}
		observed = secret
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(observed, make([]byte, 32)) {
		t.Fatal("callback-owned secret was not zeroed on return")
	}
}

func TestManagersSerializeCrossInstanceMetadataUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallets.json")
	secrets := newMemorySecrets()
	first, err := NewManager(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	go func() {
		<-start
		_, generateErr := first.Generate(context.Background(), GenerateOptions{Name: "alpha"})
		errorsChannel <- generateErr
	}()
	go func() {
		<-start
		_, generateErr := second.Generate(context.Background(), GenerateOptions{Name: "beta"})
		errorsChannel <- generateErr
	}()
	close(start)
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	wallets, err := first.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(wallets) != 2 || wallets[0].Name != "alpha" || wallets[1].Name != "beta" {
		t.Fatalf("concurrent metadata update was lost: %#v", wallets)
	}
	lockInfo, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("unsafe lock file mode: %v", lockInfo.Mode())
	}
}

func TestExpectedWalletIdentityFailsClosedAfterReplacement(t *testing.T) {
	manager, _, _ := testManager(t)
	ctx := context.Background()
	original, err := manager.Generate(ctx, GenerateOptions{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Generate(ctx, GenerateOptions{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetSignerExpected(ctx, original); !errors.Is(err, ErrWalletChanged) {
		t.Fatalf("GetSignerExpected returned %v", err)
	}
	if err := manager.RemoveExpected(ctx, original); !errors.Is(err, ErrWalletChanged) {
		t.Fatalf("RemoveExpected returned %v", err)
	}
}

func TestCheckDetectsMissingSecret(t *testing.T) {
	manager, secrets, _ := testManager(t)
	ctx := context.Background()
	if _, err := manager.Generate(ctx, GenerateOptions{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Check(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Delete(ctx, secretID("alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Check(ctx, "alpha"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Check returned %v", err)
	}
}

func TestImportMismatchDoesNotStoreOrLeakSecret(t *testing.T) {
	manager, secrets, path := testManager(t)
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := ethcrypto.FromECDSA(key)
	defer zero(secret)
	_, err = manager.Import(context.Background(), ImportOptions{
		Name: "wrong", ExpectedAddress: "0x0000000000000000000000000000000000000001",
	}, secret)
	if !errors.Is(err, ErrAddressMismatch) {
		t.Fatalf("got %v, want address mismatch", err)
	}
	if strings.Contains(err.Error(), hex.EncodeToString(secret)) {
		t.Fatal("secret appeared in import error")
	}
	if len(secrets.values) != 0 {
		t.Fatal("mismatched secret was stored")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("metadata was created after rejected import: %v", statErr)
	}
}

func TestImportFailsClosedOnStoreError(t *testing.T) {
	manager, secrets, path := testManager(t)
	secrets.setErr = errors.New("store unavailable")
	key, _ := ethcrypto.GenerateKey()
	secret := ethcrypto.FromECDSA(key)
	defer zero(secret)
	address := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
	_, err := manager.Import(context.Background(), ImportOptions{Name: "alpha", ExpectedAddress: address}, secret)
	if err == nil || strings.Contains(err.Error(), hex.EncodeToString(secret)) {
		t.Fatalf("unexpected safe-store error: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("metadata was created after store failure: %v", statErr)
	}
}

func TestValidationAndSignerMetadataMismatch(t *testing.T) {
	manager, secrets, path := testManager(t)
	ctx := context.Background()
	wallet, err := manager.Generate(ctx, GenerateOptions{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Generate(ctx, GenerateOptions{Name: "alpha"}); !errors.Is(err, ErrWalletExists) {
		t.Fatalf("duplicate wallet returned %v", err)
	}
	if _, err := manager.Generate(ctx, GenerateOptions{Name: "Bad Name"}); err == nil {
		t.Fatal("invalid name was accepted")
	}
	if _, err := manager.Generate(ctx, GenerateOptions{Name: "proxy", SignatureType: SignatureProxy}); err == nil {
		t.Fatal("proxy without funder was accepted")
	}
	other, _ := ethcrypto.GenerateKey()
	otherSecret := ethcrypto.FromECDSA(other)
	defer zero(otherSecret)
	if err := secrets.Set(ctx, secretID("alpha"), otherSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetSigner(ctx, "alpha"); !errors.Is(err, ErrAddressMismatch) {
		t.Fatalf("metadata/secret mismatch returned %v for %s", err, wallet.Address)
	}
	metadata, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), hex.EncodeToString(otherSecret)) {
		t.Fatal("replacement secret appeared in metadata")
	}
}

func FuzzParsePrivateKeyNeverPanics(f *testing.F) {
	f.Add([]byte("0x0000000000000000000000000000000000000000000000000000000000000001"))
	f.Add(make([]byte, 32))
	f.Add([]byte("not-a-key"))
	f.Fuzz(func(t *testing.T, input []byte) {
		key, normalized, _ := parsePrivateKey(input)
		zero(normalized)
		if key != nil && key.D != nil {
			key.D.SetInt64(0)
		}
	})
}
