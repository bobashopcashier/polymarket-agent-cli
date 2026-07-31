package transaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	PolygonChainID   = 137
	DefaultRPCURL    = "https://polygon.drpc.org"
	maximumRawBytes  = 256 << 10
	maximumRPCResult = 64 << 10
)

type Inspection struct {
	Hash               string `json:"hash"`
	ChainID            string `json:"chainId"`
	From               string `json:"from"`
	To                 string `json:"to,omitempty"`
	Nonce              uint64 `json:"nonce"`
	ValueWei           string `json:"valueWei"`
	Gas                uint64 `json:"gas"`
	GasPriceWei        string `json:"gasPriceWei"`
	MaxFeePerGasWei    string `json:"maxFeePerGasWei"`
	MaxPriorityFeeWei  string `json:"maxPriorityFeeWei"`
	MaxExecutionFeeWei string `json:"maxExecutionFeeWei"`
	Type               uint8  `json:"type"`
	DataBytes          int    `json:"dataBytes"`
	Selector           string `json:"selector,omitempty"`
	DataPreview        string `json:"dataPreview,omitempty"`
	DataHash           string `json:"dataHash"`
	AccessListEntries  int    `json:"accessListEntries"`
	BlobCount          int    `json:"blobCount,omitempty"`
	BlobGasFeeCapWei   string `json:"blobGasFeeCapWei,omitempty"`
	RawTransactionHash string `json:"rawTransactionHash"`
}

type Parsed struct {
	Inspection Inspection
	raw        []byte
	tx         *types.Transaction
}

func ParseFile(path string) (*Parsed, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("raw transaction file path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("could not inspect raw transaction file")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maximumRawBytes || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("raw transaction file must be a private regular non-symlink file no larger than 256 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open raw transaction file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("raw transaction file changed while opening")
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maximumRawBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maximumRawBytes {
		return nil, errors.New("could not read bounded raw transaction file")
	}
	raw, err := decodeRaw(encoded)
	zero(encoded)
	if err != nil {
		return nil, err
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		zero(raw)
		return nil, errors.New("raw transaction file does not contain a valid signed Ethereum transaction")
	}
	chainID := tx.ChainId()
	if chainID == nil || chainID.Sign() <= 0 {
		zero(raw)
		return nil, errors.New("transaction is not protected by a positive chain ID")
	}
	from, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
	if err != nil {
		zero(raw)
		return nil, errors.New("could not recover the signed transaction sender")
	}
	data := tx.Data()
	selector := ""
	dataPreview := ""
	if len(data) >= 4 {
		selector = "0x" + hex.EncodeToString(data[:4])
	}
	if len(data) != 0 {
		previewLength := len(data)
		if previewLength > 64 {
			previewLength = 64
		}
		dataPreview = "0x" + hex.EncodeToString(data[:previewLength])
	}
	rawDigest := sha256.Sum256(raw)
	maximumExecutionFee := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasFeeCap())
	inspection := Inspection{
		Hash: tx.Hash().Hex(), ChainID: chainID.String(), From: from.Hex(), Nonce: tx.Nonce(),
		ValueWei: tx.Value().String(), Gas: tx.Gas(), Type: tx.Type(), DataBytes: len(data),
		GasPriceWei: tx.GasPrice().String(), MaxFeePerGasWei: tx.GasFeeCap().String(),
		MaxPriorityFeeWei: tx.GasTipCap().String(), MaxExecutionFeeWei: maximumExecutionFee.String(),
		Selector: selector, DataPreview: dataPreview, DataHash: "0x" + hex.EncodeToString(crypto.Keccak256(data)),
		AccessListEntries: len(tx.AccessList()), BlobCount: len(tx.BlobHashes()),
		RawTransactionHash: "sha256:" + hex.EncodeToString(rawDigest[:]),
	}
	if blobFeeCap := tx.BlobGasFeeCap(); blobFeeCap != nil {
		inspection.BlobGasFeeCapWei = blobFeeCap.String()
	}
	if tx.To() != nil {
		inspection.To = tx.To().Hex()
	}
	return &Parsed{Inspection: inspection, raw: raw, tx: tx}, nil
}

func (parsed *Parsed) Close() {
	if parsed == nil {
		return
	}
	zero(parsed.raw)
	parsed.raw = nil
	parsed.tx = nil
}

type Sender struct {
	Client *http.Client
	URL    string
}

func (sender Sender) Submit(ctx context.Context, parsed *Parsed) (string, error) {
	if parsed == nil || parsed.tx == nil || len(parsed.raw) == 0 {
		return "", errors.New("signed transaction is unavailable")
	}
	if parsed.tx.ChainId().Cmp(big.NewInt(PolygonChainID)) != 0 {
		return "", fmt.Errorf("transaction chain ID must be %d", PolygonChainID)
	}
	url := sender.URL
	if url == "" {
		url = DefaultRPCURL
	}
	if url != DefaultRPCURL {
		return "", errors.New("untrusted RPC URL")
	}
	client := sender.Client
	if client == nil {
		client = &http.Client{}
	}
	chain, err := sender.rpc(ctx, client, url, "eth_chainId", []any{})
	if err != nil {
		return "", err
	}
	var chainHex string
	if json.Unmarshal(chain, &chainHex) != nil || !strings.EqualFold(chainHex, "0x89") {
		return "", errors.New("RPC endpoint did not attest Polygon chain ID 137")
	}
	rawHex := "0x" + hex.EncodeToString(parsed.raw)
	result, err := sender.rpc(ctx, client, url, "eth_sendRawTransaction", []any{rawHex})
	rawHex = ""
	if err != nil {
		return "", err
	}
	var transactionHash string
	if json.Unmarshal(result, &transactionHash) != nil || !strings.EqualFold(transactionHash, parsed.tx.Hash().Hex()) {
		return "", errors.New("RPC returned an unexpected transaction hash")
	}
	return transactionHash, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (sender Sender) rpc(ctx context.Context, client *http.Client, url, method string, parameters []any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: parameters})
	if err != nil {
		return nil, errors.New("could not encode Polygon RPC request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		zero(body)
		return nil, errors.New("could not construct Polygon RPC request")
	}
	defer zero(body)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Polygon RPC request failed before a result was available")
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maximumRPCResult+1))
	if err != nil || len(encoded) > maximumRPCResult {
		return nil, errors.New("Polygon RPC response was invalid or too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Polygon RPC returned HTTP %d", response.StatusCode)
	}
	var document rpcResponse
	if json.Unmarshal(encoded, &document) != nil || document.JSONRPC != "2.0" || document.ID != 1 {
		return nil, errors.New("Polygon RPC returned an invalid response")
	}
	if document.Error != nil {
		return nil, fmt.Errorf("Polygon RPC rejected the request with code %d", document.Error.Code)
	}
	if len(document.Result) == 0 || bytes.Equal(document.Result, []byte("null")) {
		return nil, errors.New("Polygon RPC returned no result")
	}
	return document.Result, nil
}

func decodeRaw(encoded []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) == 0 {
		return nil, errors.New("raw transaction file is empty")
	}
	if bytes.HasPrefix(trimmed, []byte("0x")) {
		trimmed = trimmed[2:]
	}
	if len(trimmed)%2 == 0 && isHex(trimmed) {
		raw := make([]byte, hex.DecodedLen(len(trimmed)))
		if _, err := hex.Decode(raw, trimmed); err != nil {
			return nil, errors.New("raw transaction hex is invalid")
		}
		return raw, nil
	}
	return append([]byte(nil), trimmed...), nil
}

func isHex(value []byte) bool {
	for _, current := range value {
		if !('0' <= current && current <= '9' || 'a' <= current && current <= 'f' || 'A' <= current && current <= 'F') {
			return false
		}
	}
	return true
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
