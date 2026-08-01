package wallet

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"sync"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// Signer intentionally exposes signing operations rather than the underlying
// private key. It is never JSON serializable.
type Signer struct {
	mu       sync.Mutex
	key      *ecdsa.PrivateKey
	metadata PublicWallet
}

func (s *Signer) Address() string {
	if s == nil {
		return ""
	}
	return s.metadata.Address
}

func (s *Signer) Wallet() PublicWallet {
	if s == nil {
		return PublicWallet{}
	}
	return s.metadata
}

// SignDigest signs exactly one 32-byte digest and returns [R || S || V].
func (s *Signer) SignDigest(digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("digest must be exactly 32 bytes")
	}
	if s == nil {
		return nil, ErrSignerDestroyed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil || s.key.D == nil || s.key.D.Sign() == 0 {
		return nil, ErrSignerDestroyed
	}
	signature, err := ethcrypto.Sign(digest, s.key)
	if err != nil {
		return nil, fmt.Errorf("digest signing failed")
	}
	return signature, nil
}

// Destroy best-effort clears the key held by this Signer. Callers should keep
// signer lifetimes short and defer Destroy immediately after acquisition.
func (s *Signer) Destroy() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil && s.key.D != nil {
		s.key.D.SetInt64(0)
	}
	s.key = nil
}

func (*Signer) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("wallet signer cannot be serialized")
}

func (*Signer) MarshalText() ([]byte, error) {
	return nil, fmt.Errorf("wallet signer cannot be serialized")
}

var _ json.Marshaler = (*Signer)(nil)
