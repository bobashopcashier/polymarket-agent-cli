package wallet

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gofrs/flock"
)

type Manager struct {
	metadataPath string
	secrets      SecretStore
	mu           sync.Mutex
}

func NewManager(metadataPath string, secrets SecretStore) (*Manager, error) {
	if secrets == nil {
		return nil, fmt.Errorf("wallet secret store is required")
	}
	if metadataPath == "" || !filepath.IsAbs(metadataPath) || filepath.Clean(metadataPath) != metadataPath {
		return nil, ErrUnsafeMetadata
	}
	if filepath.Base(metadataPath) == "." || filepath.Base(metadataPath) == string(filepath.Separator) {
		return nil, ErrUnsafeMetadata
	}
	return &Manager{metadataPath: metadataPath, secrets: secrets}, nil
}

func New(metadataPath string, secrets SecretStore) (*Manager, error) {
	return NewManager(metadataPath, secrets)
}

func (m *Manager) Generate(ctx context.Context, options GenerateOptions) (PublicWallet, error) {
	if err := contextResult(ctx); err != nil {
		return PublicWallet{}, err
	}
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		return PublicWallet{}, fmt.Errorf("generate wallet key failed")
	}
	defer key.D.SetInt64(0)
	secret := ethcrypto.FromECDSA(key)
	defer zero(secret)
	address := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
	return m.create(ctx, ImportOptions{
		Name: options.Name, ExpectedAddress: address, SignatureType: options.SignatureType,
		Funder: options.Funder, SetActive: options.SetActive,
	}, key, secret)
}

// Import accepts secret bytes directly. Callers are responsible for obtaining
// them from hidden TTY input, an inherited descriptor, or another secret-safe
// channel. The API deliberately has no argv or string-secret form.
func (m *Manager) Import(ctx context.Context, options ImportOptions, secret []byte) (PublicWallet, error) {
	if err := contextResult(ctx); err != nil {
		return PublicWallet{}, err
	}
	key, normalizedSecret, err := parsePrivateKey(secret)
	if err != nil {
		return PublicWallet{}, err
	}
	defer key.D.SetInt64(0)
	defer zero(normalizedSecret)
	return m.create(ctx, options, key, normalizedSecret)
}

func (m *Manager) create(ctx context.Context, options ImportOptions, key *ecdsa.PrivateKey, secret []byte) (PublicWallet, error) {
	if strings.TrimSpace(options.ExpectedAddress) == "" {
		return PublicWallet{}, fmt.Errorf("expected address is required")
	}
	wallet, err := normalizePublicWallet(PublicWallet{
		Name: options.Name, Address: ethcrypto.PubkeyToAddress(key.PublicKey).Hex(),
		SignatureType: options.SignatureType, Funder: options.Funder,
	})
	if err != nil {
		return PublicWallet{}, err
	}
	expected, err := canonicalAddress(options.ExpectedAddress)
	if err != nil {
		return PublicWallet{}, fmt.Errorf("expected address is invalid")
	}
	if wallet.Address != expected {
		return PublicWallet{}, ErrAddressMismatch
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.lockMetadata(ctx)
	if err != nil {
		return PublicWallet{}, err
	}
	defer release()
	metadata, err := loadMetadata(m.metadataPath)
	if err != nil {
		return PublicWallet{}, err
	}
	if _, exists := metadata.Wallets[wallet.Name]; exists {
		return PublicWallet{}, ErrWalletExists
	}
	secretCopy := append([]byte(nil), secret...)
	if err := m.secrets.Set(ctx, secretID(wallet.Name), secretCopy); err != nil {
		zero(secretCopy)
		return PublicWallet{}, safeSecretStoreError(err)
	}
	zero(secretCopy)
	metadata.Wallets[wallet.Name] = wallet
	if metadata.ActiveWallet == "" || options.SetActive {
		metadata.ActiveWallet = wallet.Name
	}
	if err := writeMetadata(m.metadataPath, metadata); err != nil {
		deleteErr := m.secrets.Delete(withoutCancel(ctx), secretID(wallet.Name))
		if deleteErr != nil && !errors.Is(deleteErr, ErrSecretNotFound) {
			return PublicWallet{}, fmt.Errorf("write wallet metadata failed and secret rollback failed")
		}
		return PublicWallet{}, err
	}
	return wallet, nil
}

func (m *Manager) List(ctx context.Context) ([]PublicWallet, error) {
	if err := contextResult(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	metadata, err := loadMetadata(m.metadataPath)
	if err != nil {
		return nil, err
	}
	result := make([]PublicWallet, 0, len(metadata.Wallets))
	for _, wallet := range metadata.Wallets {
		result = append(result, wallet)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (m *Manager) Show(ctx context.Context, name string) (PublicWallet, error) {
	if err := contextResult(ctx); err != nil {
		return PublicWallet{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	metadata, err := loadMetadata(m.metadataPath)
	if err != nil {
		return PublicWallet{}, err
	}
	if name == "" {
		name = metadata.ActiveWallet
		if name == "" {
			return PublicWallet{}, ErrNoActiveWallet
		}
	}
	wallet, exists := metadata.Wallets[name]
	if !exists {
		return PublicWallet{}, ErrWalletNotFound
	}
	return wallet, nil
}

func (m *Manager) Active(ctx context.Context) (PublicWallet, error) {
	return m.Show(ctx, "")
}

func (m *Manager) Use(ctx context.Context, name string) (PublicWallet, error) {
	return m.use(ctx, name, nil)
}

// UseExpected selects a profile only if its public identity still matches a
// previously reviewed plan.
func (m *Manager) UseExpected(ctx context.Context, expected PublicWallet) (PublicWallet, error) {
	return m.use(ctx, expected.Name, &expected)
}

func (m *Manager) use(ctx context.Context, name string, expected *PublicWallet) (PublicWallet, error) {
	if !walletNamePattern.MatchString(name) {
		return PublicWallet{}, ErrWalletNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.lockMetadata(ctx)
	if err != nil {
		return PublicWallet{}, err
	}
	defer release()
	metadata, err := loadMetadata(m.metadataPath)
	if err != nil {
		return PublicWallet{}, err
	}
	wallet, exists := metadata.Wallets[name]
	if !exists {
		return PublicWallet{}, ErrWalletNotFound
	}
	if expected != nil && wallet != *expected {
		return PublicWallet{}, ErrWalletChanged
	}
	if metadata.ActiveWallet == name {
		return wallet, nil
	}
	metadata.ActiveWallet = name
	if err := writeMetadata(m.metadataPath, metadata); err != nil {
		return PublicWallet{}, err
	}
	return wallet, nil
}

func (m *Manager) Remove(ctx context.Context, name string) error {
	return m.remove(ctx, name, nil)
}

// RemoveExpected removes a profile only if its public identity still matches a
// previously reviewed plan.
func (m *Manager) RemoveExpected(ctx context.Context, expected PublicWallet) error {
	return m.remove(ctx, expected.Name, &expected)
}

func (m *Manager) remove(ctx context.Context, name string, expected *PublicWallet) error {
	if !walletNamePattern.MatchString(name) {
		return ErrWalletNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.lockMetadata(ctx)
	if err != nil {
		return err
	}
	defer release()
	metadata, err := loadMetadata(m.metadataPath)
	if err != nil {
		return err
	}
	entry, exists := metadata.Wallets[name]
	if !exists {
		return ErrWalletNotFound
	}
	if expected != nil && entry != *expected {
		return ErrWalletChanged
	}
	secret, err := m.secrets.Get(ctx, secretID(name))
	if err != nil {
		return safeSecretStoreError(err)
	}
	defer zero(secret)
	if err := m.secrets.Delete(ctx, secretID(name)); err != nil {
		return safeSecretStoreError(err)
	}
	delete(metadata.Wallets, name)
	if metadata.ActiveWallet == name {
		metadata.ActiveWallet = ""
	}
	if err := writeMetadata(m.metadataPath, metadata); err != nil {
		restoreSecret := append([]byte(nil), secret...)
		restoreErr := m.secrets.Set(withoutCancel(ctx), secretID(name), restoreSecret)
		zero(restoreSecret)
		if restoreErr != nil {
			return fmt.Errorf("write wallet metadata failed and secret rollback failed")
		}
		return err
	}
	return nil
}

func (m *Manager) GetSigner(ctx context.Context, name string) (*Signer, error) {
	return m.getSigner(ctx, name, nil)
}

// GetSignerExpected loads a signer only if its public identity still matches a
// previously reviewed plan.
func (m *Manager) GetSignerExpected(ctx context.Context, expected PublicWallet) (*Signer, error) {
	return m.getSigner(ctx, expected.Name, &expected)
}

func (m *Manager) getSigner(ctx context.Context, name string, expected *PublicWallet) (*Signer, error) {
	var signer *Signer
	err := m.withSecret(ctx, name, expected, func(entry PublicWallet, secret []byte) error {
		key, normalized, err := parsePrivateKey(secret)
		if err != nil {
			return fmt.Errorf("load wallet signer: %w", ErrInvalidSecret)
		}
		zero(normalized)
		signer = &Signer{key: key, metadata: entry}
		return nil
	})
	return signer, err
}

// WithSecret retrieves a wallet secret for the duration of callback only. The
// callback receives caller-owned raw key bytes that are zeroed before this
// method returns. It must not retain the slice. Public metadata is revalidated
// against the secret before the callback runs.
func (m *Manager) WithSecret(ctx context.Context, name string, callback func(PublicWallet, []byte) error) error {
	if callback == nil {
		return fmt.Errorf("wallet secret callback is required")
	}
	return m.withSecret(ctx, name, nil, callback)
}

// WithSecretExpected scopes a wallet secret only if public metadata still
// matches a previously reviewed plan.
func (m *Manager) WithSecretExpected(ctx context.Context, expected PublicWallet, callback func(PublicWallet, []byte) error) error {
	if callback == nil {
		return fmt.Errorf("wallet secret callback is required")
	}
	return m.withSecret(ctx, expected.Name, &expected, callback)
}

// Check verifies that a profile's keychain secret is present, parseable, and
// matches its public address without returning key material.
func (m *Manager) Check(ctx context.Context, name string) (PublicWallet, error) {
	var checked PublicWallet
	err := m.withSecret(ctx, name, nil, func(entry PublicWallet, _ []byte) error {
		checked = entry
		return nil
	})
	return checked, err
}

func (m *Manager) withSecret(ctx context.Context, name string, expected *PublicWallet, callback func(PublicWallet, []byte) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.lockMetadata(ctx)
	if err != nil {
		return err
	}
	defer release()
	metadata, err := loadMetadata(m.metadataPath)
	if err != nil {
		return err
	}
	if name == "" {
		name = metadata.ActiveWallet
		if name == "" {
			return ErrNoActiveWallet
		}
	}
	wallet, exists := metadata.Wallets[name]
	if !exists {
		return ErrWalletNotFound
	}
	if expected != nil && wallet != *expected {
		return ErrWalletChanged
	}
	secret, err := m.secrets.Get(ctx, secretID(wallet.Name))
	if err != nil {
		return safeSecretStoreError(err)
	}
	defer zero(secret)
	key, normalized, err := parsePrivateKey(secret)
	if err != nil {
		return fmt.Errorf("load wallet secret: %w", ErrInvalidSecret)
	}
	defer key.D.SetInt64(0)
	defer zero(normalized)
	if ethcrypto.PubkeyToAddress(key.PublicKey).Hex() != wallet.Address {
		return ErrAddressMismatch
	}
	return callback(wallet, normalized)
}

func (m *Manager) lockMetadata(ctx context.Context) (func(), error) {
	directory := filepath.Dir(m.metadataPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create wallet metadata directory: %w", err)
	}
	lock := flock.New(m.metadataPath+".lock", flock.SetPermissions(0o600))
	if ctx == nil {
		ctx = context.Background()
	}
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		_ = lock.Close()
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("lock wallet metadata failed")
	}
	info, statErr := os.Lstat(m.metadataPath + ".lock")
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = lock.Unlock()
		_ = lock.Close()
		return nil, ErrUnsafeMetadata
	}
	return func() {
		_ = lock.Unlock()
		_ = lock.Close()
	}, nil
}

func parsePrivateKey(secret []byte) (*ecdsa.PrivateKey, []byte, error) {
	var normalized []byte
	if len(secret) == 32 {
		normalized = append([]byte(nil), secret...)
	} else {
		text := bytes.TrimSpace(secret)
		text = bytes.TrimPrefix(text, []byte("0x"))
		if len(text) != 64 {
			return nil, nil, ErrInvalidSecret
		}
		decoded := make([]byte, 32)
		_, err := hex.Decode(decoded, text)
		if err != nil {
			zero(decoded)
			return nil, nil, ErrInvalidSecret
		}
		normalized = decoded
	}
	key, err := ethcrypto.ToECDSA(normalized)
	if err != nil {
		zero(normalized)
		return nil, nil, ErrInvalidSecret
	}
	return key, normalized, nil
}

func secretID(name string) string { return "wallet/" + name }

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func withoutCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func safeSecretStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSecretNotFound):
		return ErrSecretNotFound
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrKeyringUnavailable):
		return ErrKeyringUnavailable
	default:
		return ErrSecretStore
	}
}
