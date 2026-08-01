package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const maximumMetadataBytes = 1 << 20

func emptyMetadata() Metadata {
	return Metadata{
		SchemaVersion: MetadataSchemaVersion,
		Version:       MetadataVersion,
		Wallets:       make(map[string]PublicWallet),
	}
}

func loadMetadata(path string) (Metadata, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyMetadata(), nil
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("inspect wallet metadata: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o077 != 0 {
		return Metadata{}, ErrUnsafeMetadata
	}
	file, err := os.Open(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("open wallet metadata: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Metadata{}, fmt.Errorf("inspect open wallet metadata: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return Metadata{}, ErrUnsafeMetadata
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumMetadataBytes+1))
	if err != nil {
		return Metadata{}, fmt.Errorf("read wallet metadata: %w", err)
	}
	if len(data) == 0 || len(data) > maximumMetadataBytes {
		return Metadata{}, ErrInvalidMetadata
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Metadata{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var metadata Metadata
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode wallet metadata: %w", ErrInvalidMetadata)
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return Metadata{}, ErrInvalidMetadata
	}
	if err := validateMetadata(metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func writeMetadata(path string, metadata Metadata) error {
	if err := validateMetadata(metadata); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return ErrUnsafeMetadata
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect wallet metadata target: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create wallet metadata directory: %w", err)
	}
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode wallet metadata: %w", err)
	}
	payload = append(payload, '\n')
	if len(payload) > maximumMetadataBytes {
		return ErrInvalidMetadata
	}
	temporary, err := os.CreateTemp(directory, ".wallets-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary wallet metadata: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary wallet metadata: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write temporary wallet metadata: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary wallet metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary wallet metadata: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace wallet metadata: %w", err)
	}
	cleanup = false
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure wallet metadata: %w", err)
	}
	if directoryHandle, openErr := os.Open(directory); openErr == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}

func validateMetadata(metadata Metadata) error {
	if metadata.SchemaVersion != MetadataSchemaVersion || metadata.Version != MetadataVersion || metadata.Wallets == nil {
		return ErrInvalidMetadata
	}
	if metadata.ActiveWallet != "" {
		if _, exists := metadata.Wallets[metadata.ActiveWallet]; !exists {
			return ErrInvalidMetadata
		}
	}
	for name, entry := range metadata.Wallets {
		if name != entry.Name || !walletNamePattern.MatchString(name) {
			return ErrInvalidMetadata
		}
		normalized, err := normalizePublicWallet(entry)
		if err != nil || normalized != entry {
			return ErrInvalidMetadata
		}
	}
	return nil
}

func normalizePublicWallet(wallet PublicWallet) (PublicWallet, error) {
	if !walletNamePattern.MatchString(wallet.Name) {
		return PublicWallet{}, fmt.Errorf("invalid wallet name")
	}
	address, err := canonicalAddress(wallet.Address)
	if err != nil {
		return PublicWallet{}, err
	}
	signatureType := wallet.SignatureType
	if signatureType == "" {
		signatureType = SignatureEOA
	}
	result := PublicWallet{Name: wallet.Name, Address: address, SignatureType: signatureType}
	switch signatureType {
	case SignatureEOA:
		if strings.TrimSpace(wallet.Funder) != "" {
			return PublicWallet{}, fmt.Errorf("EOA wallet must not declare a funder")
		}
	case SignatureProxy, SignatureGnosisSafe:
		funder, funderErr := canonicalAddress(wallet.Funder)
		if funderErr != nil {
			return PublicWallet{}, fmt.Errorf("proxy or safe wallet requires a valid funder")
		}
		result.Funder = funder
	default:
		return PublicWallet{}, fmt.Errorf("unsupported wallet signature type")
	}
	return result, nil
}

func canonicalAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !common.IsHexAddress(value) {
		return "", fmt.Errorf("invalid Ethereum address")
	}
	return common.HexToAddress(value).Hex(), nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, 0); err != nil {
		return ErrInvalidMetadata
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return ErrInvalidMetadata
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalidMetadata
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}
			key, stringKey := keyToken.(string)
			if !stringKey {
				return ErrInvalidMetadata
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidMetadata
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return ErrInvalidMetadata
	}
}
