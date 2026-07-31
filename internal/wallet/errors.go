package wallet

import "errors"

var (
	ErrWalletNotFound     = errors.New("wallet not found")
	ErrWalletExists       = errors.New("wallet already exists")
	ErrWalletChanged      = errors.New("wallet metadata changed after authorization")
	ErrNoActiveWallet     = errors.New("no active wallet")
	ErrSecretNotFound     = errors.New("wallet secret not found")
	ErrSecretStore        = errors.New("wallet secret store failed")
	ErrAddressMismatch    = errors.New("private key does not match expected address")
	ErrInvalidSecret      = errors.New("invalid secp256k1 private key")
	ErrSignerDestroyed    = errors.New("wallet signer has been destroyed")
	ErrUnsafeMetadata     = errors.New("wallet metadata path is unsafe")
	ErrInvalidMetadata    = errors.New("wallet metadata is invalid")
	ErrKeyringUnavailable = errors.New("OS keyring is unavailable")
)
