package transaction

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func signedTransaction(t *testing.T) ([]byte, *types.Transaction, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(PolygonChainID), Nonce: 7, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), Gas: 21000, To: &to, Value: big.NewInt(3)})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(PolygonChainID)), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw, signed, key
}

func TestParseFileAndSubmit(t *testing.T) {
	raw, tx, key := signedTransaction(t)
	defer zero(raw)
	path := filepath.Join(t.TempDir(), "tx.hex")
	if err := os.WriteFile(path, []byte("0x"+hex.EncodeToString(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.Close()
	if parsed.Inspection.Hash != tx.Hash().Hex() || parsed.Inspection.ChainID != "137" || parsed.Inspection.From != crypto.PubkeyToAddress(key.PublicKey).Hex() {
		t.Fatalf("unexpected inspection: %#v", parsed.Inspection)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		result := `"0x89"`
		if strings.Contains(string(body), "eth_sendRawTransaction") {
			result = `"` + tx.Hash().Hex() + `"`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`)), Header: make(http.Header)}, nil
	})}
	got, err := (Sender{Client: client}).Submit(context.Background(), parsed)
	if err != nil || got != tx.Hash().Hex() {
		t.Fatalf("hash=%s err=%v", got, err)
	}
}

func TestParseFileRejectsRelativeAndSymlink(t *testing.T) {
	if _, err := ParseFile("relative.hex"); err == nil {
		t.Fatal("expected relative path rejection")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("00"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := target + ".link"
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(link); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestParseFileRejectsGroupOrWorldReadableFile(t *testing.T) {
	raw, _, _ := signedTransaction(t)
	defer zero(raw)
	path := filepath.Join(t.TempDir(), "tx.hex")
	if err := os.WriteFile(path, []byte("0x"+hex.EncodeToString(raw)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(path); err == nil {
		t.Fatal("expected permissive mode rejection")
	}
}

func TestSubmitRejectsWrongChain(t *testing.T) {
	key, _ := crypto.GenerateKey()
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")
	tx := types.NewTx(&types.LegacyTx{Nonce: 1, Gas: 21000, To: &to})
	signed, _ := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(1)), key)
	raw, _ := signed.MarshalBinary()
	parsed := &Parsed{raw: raw, tx: signed}
	defer parsed.Close()
	if _, err := (Sender{}).Submit(context.Background(), parsed); err == nil {
		t.Fatal("expected chain rejection")
	}
}
