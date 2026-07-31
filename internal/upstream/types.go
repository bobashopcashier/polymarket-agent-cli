// Package upstream provides a narrow process boundary around the official
// Polymarket CLI. Callers can execute only commands constructed by this
// package; arbitrary argv and hosts are intentionally not supported.
package upstream

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	ProductionCLOBHost = "https://clob.polymarket.com"
	ProductionRPCURL   = "https://polygon.drpc.org"
	// RequiredRevision is the official polymarket-cli revision that merged
	// production CLOB V2 support. Packaged v0.1.x releases predate this commit.
	RequiredRevision = "9b18b5faf5493b945c48ca22efaf9645f0c69ab8"

	defaultTimeout        = 30 * time.Second
	defaultStdoutMaxBytes = int64(8 << 20)
	defaultStderrMaxBytes = int64(64 << 10)
	maximumOutputBytes    = int64(64 << 20)
)

// Options controls local process behavior. ExecutablePath must be an explicit
// absolute path; the PATH is never searched.
type Options struct {
	ExecutablePath string
	Timeout        time.Duration
	StdoutMaxBytes int64
	StderrMaxBytes int64
}

// SignatureType is one of the exact signer modes accepted by the official
// CLI. Unknown values are rejected instead of falling back to EOA behavior.
type SignatureType string

const (
	SignatureEOA        SignatureType = "eoa"
	SignatureProxy      SignatureType = "proxy"
	SignatureGnosisSafe SignatureType = "gnosis-safe"
)

func (s SignatureType) valid() bool {
	switch s {
	case SignatureEOA, SignatureProxy, SignatureGnosisSafe:
		return true
	default:
		return false
	}
}

// Credentials are supplied per invocation and are never retained by Runner.
// PrivateKey must contain 32 raw bytes or a 0x-prefixed, 32-byte hexadecimal
// key. It is normalized and passed only through the child process environment,
// never argv.
type Credentials struct {
	PrivateKey    []byte
	SignatureType SignatureType
}

// ApprovalOperation describes one on-chain approval emitted by the pinned
// official approve-set implementation. Values are public and included in pmx
// plans so blanket approvals are never summarized as an opaque scope.
type ApprovalOperation struct {
	Step          int    `json:"step"`
	Type          string `json:"type"`
	TokenContract string `json:"tokenContract"`
	TargetName    string `json:"targetName"`
	TargetAddress string `json:"targetAddress"`
	Amount        string `json:"amount,omitempty"`
	Approved      *bool  `json:"approved,omitempty"`
}

const maximumUint256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

// ApprovalOperations returns a defensive copy of the exact eleven approvals
// issued by RequiredRevision for an EOA wallet on Polygon.
func ApprovalOperations() []ApprovalOperation {
	type target struct {
		name       string
		address    string
		collateral bool
		operator   bool
	}
	targets := []target{
		{"CTF Exchange", "0xE111180000d2663C0091e4f400237545B87B996B", true, true},
		{"Neg Risk Exchange", "0xe2222d279d744050d28e00520010520000310F59", true, true},
		{"Neg Risk Adapter", "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296", true, true},
		{"Conditional Tokens", "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045", true, false},
		{"CTF Collateral Adapter", "0xADa100874d00e3331D00F2007a9c336a65009718", true, true},
		{"Neg Risk CTF Collateral Adapter", "0xAdA200001000ef00D07553cEE7006808F895c6F1", true, true},
	}
	operations := make([]ApprovalOperation, 0, 11)
	approved := true
	for _, current := range targets {
		if current.collateral {
			operations = append(operations, ApprovalOperation{
				Step: len(operations) + 1, Type: "erc20-approve",
				TokenContract: "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB",
				TargetName:    current.name, TargetAddress: current.address, Amount: maximumUint256,
			})
		}
		if current.operator {
			operations = append(operations, ApprovalOperation{
				Step: len(operations) + 1, Type: "erc1155-set-approval-for-all",
				TokenContract: "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045",
				TargetName:    current.name, TargetAddress: current.address, Approved: &approved,
			})
		}
	}
	return operations
}

// Command is an allowlisted upstream operation. Its argv and authentication
// requirement are private so callers cannot construct arbitrary invocations.
type Command struct {
	operation      string
	args           []string
	requiresSecret bool
}

// Operation returns the stable, non-secret operation name used in errors and
// audit events.
func (c Command) Operation() string { return c.operation }

// ErrorKind is a stable machine-readable process failure classification.
type ErrorKind string

const (
	ErrorInvalidConfig   ErrorKind = "invalid_config"
	ErrorInvalidCommand  ErrorKind = "invalid_command"
	ErrorMissingSecret   ErrorKind = "missing_secret"
	ErrorCanceled        ErrorKind = "canceled"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorStartFailed     ErrorKind = "start_failed"
	ErrorExecutionFailed ErrorKind = "execution_failed"
	ErrorOutputTooLarge  ErrorKind = "output_too_large"
	ErrorInvalidJSON     ErrorKind = "invalid_json"
)

// Error contains only bounded, redacted values. It deliberately does not
// retain the raw os/exec error, stderr, argv, environment, or credentials.
type Error struct {
	Kind      ErrorKind `json:"kind"`
	Operation string    `json:"operation,omitempty"`
	ExitCode  int       `json:"exitCode,omitempty"`
	Stream    string    `json:"stream,omitempty"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := string(e.Kind)
	if e.Operation != "" {
		prefix = e.Operation + ": " + prefix
	}
	if e.Message == "" {
		return prefix
	}
	return fmt.Sprintf("%s: %s", prefix, e.Message)
}

// Result is decoded, sanitized JSON produced by the upstream CLI. Raw is
// compact JSON and is safe to embed directly in the caller's output envelope.
type Result struct {
	Raw json.RawMessage
}
