package wallet

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
)

var (
	secretIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]{0,127}$`)
	servicePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// KeyringStore stores opaque bytes in the platform keyring. Values are encoded
// because OS keyrings expose a string API.
type KeyringStore struct {
	service string
}

func NewKeyringStore(service string) (*KeyringStore, error) {
	service = strings.TrimSpace(service)
	if !servicePattern.MatchString(service) {
		return nil, fmt.Errorf("invalid keyring service name")
	}
	return &KeyringStore{service: service}, nil
}

func (s *KeyringStore) Set(ctx context.Context, id string, secret []byte) error {
	if err := validateSecretRequest(ctx, id); err != nil {
		return err
	}
	if len(secret) == 0 {
		return ErrInvalidSecret
	}
	encoded := base64.RawStdEncoding.EncodeToString(secret)
	if err := keyring.Set(s.service, id, encoded); err != nil {
		if errors.Is(err, keyring.ErrUnsupportedPlatform) {
			return ErrKeyringUnavailable
		}
		return ErrSecretStore
	}
	return nil
}

func (s *KeyringStore) Get(ctx context.Context, id string) ([]byte, error) {
	if err := validateSecretRequest(ctx, id); err != nil {
		return nil, err
	}
	encoded, err := keyring.Get(s.service, id)
	if err != nil {
		switch {
		case errors.Is(err, keyring.ErrNotFound):
			return nil, ErrSecretNotFound
		case errors.Is(err, keyring.ErrUnsupportedPlatform):
			return nil, ErrKeyringUnavailable
		default:
			return nil, ErrSecretStore
		}
	}
	secret, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("keyring entry is corrupted")
	}
	if err := contextResult(ctx); err != nil {
		zero(secret)
		return nil, err
	}
	return secret, nil
}

func (s *KeyringStore) Delete(ctx context.Context, id string) error {
	if err := validateSecretRequest(ctx, id); err != nil {
		return err
	}
	if err := keyring.Delete(s.service, id); err != nil {
		switch {
		case errors.Is(err, keyring.ErrNotFound):
			return ErrSecretNotFound
		case errors.Is(err, keyring.ErrUnsupportedPlatform):
			return ErrKeyringUnavailable
		default:
			return ErrSecretStore
		}
	}
	return nil
}

func validateSecretRequest(ctx context.Context, id string) error {
	if err := contextResult(ctx); err != nil {
		return err
	}
	if !secretIDPattern.MatchString(id) {
		return fmt.Errorf("invalid keyring secret identifier")
	}
	return nil
}

func contextResult(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
