package wallet

import (
	"context"
	"regexp"
)

const (
	MetadataSchemaVersion = "pmx.wallet-metadata/v1"
	MetadataVersion       = 1
)

type SignatureType string

const (
	SignatureEOA        SignatureType = "eoa"
	SignatureProxy      SignatureType = "proxy"
	SignatureGnosisSafe SignatureType = "gnosis-safe"
)

type PublicWallet struct {
	Name          string        `json:"name"`
	Address       string        `json:"address"`
	SignatureType SignatureType `json:"signatureType"`
	Funder        string        `json:"funder,omitempty"`
}

type Metadata struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Version       int                     `json:"version"`
	ActiveWallet  string                  `json:"activeWallet,omitempty"`
	Wallets       map[string]PublicWallet `json:"wallets"`
}

type ImportOptions struct {
	Name            string
	ExpectedAddress string
	SignatureType   SignatureType
	Funder          string
	SetActive       bool
}

type GenerateOptions struct {
	Name          string
	SignatureType SignatureType
	Funder        string
	SetActive     bool
}

// SecretStore is the only boundary through which private key bytes enter or
// leave the manager. Implementations must copy input bytes before Set returns,
// return caller-owned bytes from Get, and never include secret values in errors.
type SecretStore interface {
	Set(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

var walletNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
