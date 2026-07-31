package upstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var protectedEnvironment = map[string]struct{}{
	"POLYMARKET_PRIVATE_KEY":    {},
	"POLYMARKET_CLOB_HOST":      {},
	"POLYMARKET_RPC_URL":        {},
	"POLYMARKET_SIGNATURE_TYPE": {},
}

var inheritedEnvironment = map[string]struct{}{
	"HOME": {}, "USER": {}, "LOGNAME": {}, "LANG": {}, "LC_ALL": {},
	"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {},
}

// Runner executes the official CLI through a fixed, bounded process boundary.
type Runner struct {
	executablePath string
	executableHash string
	timeout        time.Duration
	stdoutMaxBytes int64
	stderrMaxBytes int64
}

// ExecutablePath returns the validated public path to the execution engine.
// It never includes credentials or invocation arguments.
func (r *Runner) ExecutablePath() string {
	if r == nil {
		return ""
	}
	return r.executablePath
}

// ExecutableSHA256 returns the digest captured when the runner was created.
func (r *Runner) ExecutableSHA256() string {
	if r == nil {
		return ""
	}
	return r.executableHash
}

func New(options Options) (*Runner, error) {
	if err := validateExecutable(options.ExecutablePath); err != nil {
		return nil, &Error{Kind: ErrorInvalidConfig, Message: sanitizeErrorText(err.Error(), "")}
	}
	executableHash, err := hashExecutable(options.ExecutablePath)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidConfig, Message: "could not hash executable"}
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < time.Millisecond || timeout > 5*time.Minute {
		return nil, &Error{Kind: ErrorInvalidConfig, Message: "timeout must be between 1ms and 5m"}
	}
	stdoutMax, err := outputLimit(options.StdoutMaxBytes, defaultStdoutMaxBytes)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidConfig, Message: "stdout limit " + err.Error()}
	}
	stderrMax, err := outputLimit(options.StderrMaxBytes, defaultStderrMaxBytes)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidConfig, Message: "stderr limit " + err.Error()}
	}
	return &Runner{
		executablePath: options.ExecutablePath,
		executableHash: executableHash,
		timeout:        timeout,
		stdoutMaxBytes: stdoutMax,
		stderrMaxBytes: stderrMax,
	}, nil
}

func outputLimit(value, fallback int64) (int64, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < 1 || value > maximumOutputBytes {
		return 0, fmt.Errorf("must be between 1 and %d bytes", maximumOutputBytes)
	}
	return value, nil
}

func validateExecutable(path string) error {
	if path == "" {
		return fmt.Errorf("executable path is required")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("executable path must be absolute and clean")
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("could not inspect executable")
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("executable path must not be a symlink")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("could not inspect executable")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("executable path is not a regular file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("executable path is not executable")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("executable path must not be group- or world-writable")
	}
	if info.Size() <= 0 || info.Size() > 256<<20 {
		return fmt.Errorf("executable size is outside the allowed range")
	}
	return nil
}

func hashExecutable(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, (256<<20)+1)); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

// Run executes an allowlisted command and returns compact sanitized JSON.
// Credentials are required for authenticated commands and rejected for public
// commands so secrets are not exposed to a child unnecessarily.
func (r *Runner) Run(ctx context.Context, command Command, credentials *Credentials) (Result, error) {
	if ctx == nil {
		return Result{}, &Error{Kind: ErrorInvalidCommand, Operation: command.operation, Message: "context is nil"}
	}
	if command.operation == "" || len(command.args) == 0 {
		return Result{}, &Error{Kind: ErrorInvalidCommand, Message: "command was not built by upstream"}
	}
	executablePath, cleanupExecutable, err := r.stageVerifiedExecutable()
	if err != nil {
		return Result{}, &Error{Kind: ErrorInvalidConfig, Operation: command.operation, Message: "executable identity changed after configuration"}
	}
	defer cleanupExecutable()

	secret, signatureType, err := resolveCredentials(command, credentials)
	if err != nil {
		return Result{}, err
	}

	runContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	stdout := newBoundedCapture(r.stdoutMaxBytes, cancel)
	stderr := newBoundedCapture(r.stderrMaxBytes, cancel)

	args := make([]string, 0, len(command.args)+2)
	args = append(args, "--output", "json")
	args = append(args, command.args...)
	// Execute a private copy made from the exact inode that was just hashed.
	// Reopening the configured pathname would permit an atomic replacement race
	// before the secret-bearing exec.
	process := exec.CommandContext(runContext, executablePath, args...)
	process.WaitDelay = 2 * time.Second
	process.Stdout = stdout
	process.Stderr = stderr
	process.Env = childEnvironment(secret, signatureType)

	runErr := process.Run()
	process.Env = nil

	if stdout.exceeded {
		return Result{}, &Error{Kind: ErrorOutputTooLarge, Operation: command.operation, Stream: "stdout", Message: fmt.Sprintf("upstream stdout exceeded %d-byte limit", r.stdoutMaxBytes)}
	}
	if stderr.exceeded {
		return Result{}, &Error{Kind: ErrorOutputTooLarge, Operation: command.operation, Stream: "stderr", Message: fmt.Sprintf("upstream stderr exceeded %d-byte limit", r.stderrMaxBytes)}
	}
	if runErr != nil {
		return Result{}, classifyRunError(ctx, runContext, command.operation, runErr, stderr.buffer.String(), secret)
	}

	safeJSON, decodeErr := sanitizeJSON(stdout.buffer.Bytes(), secret)
	if decodeErr != nil {
		return Result{}, &Error{Kind: ErrorInvalidJSON, Operation: command.operation, Message: "upstream returned invalid JSON"}
	}
	return Result{Raw: safeJSON}, nil
}

func (r *Runner) stageVerifiedExecutable() (string, func(), error) {
	if err := validateExecutable(r.executablePath); err != nil {
		return "", nil, err
	}
	pathInfo, err := os.Lstat(r.executablePath)
	if err != nil {
		return "", nil, err
	}
	file, err := os.Open(r.executablePath)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	openInfo, err := file.Stat()
	if err != nil || !os.SameFile(pathInfo, openInfo) {
		return "", nil, errors.New("executable changed while opening")
	}
	directory, err := os.MkdirTemp("", ".pmx-engine-")
	if err != nil {
		return "", nil, errors.New("could not create private executable directory")
	}
	cleanup := func() {
		_ = os.Remove(filepath.Join(directory, "polymarket"))
		_ = os.Remove(directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		cleanup()
		return "", nil, errors.New("could not secure executable directory")
	}
	stagedPath := filepath.Join(directory, "polymarket")
	staged, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if err != nil {
		cleanup()
		return "", nil, errors.New("could not stage executable")
	}
	written, copyErr := io.Copy(staged, io.LimitReader(file, (256<<20)+1))
	syncErr := staged.Sync()
	closeErr := staged.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != openInfo.Size() {
		cleanup()
		return "", nil, errors.New("could not stage complete executable")
	}
	if err := os.Chmod(stagedPath, 0o500); err != nil {
		cleanup()
		return "", nil, errors.New("could not secure staged executable")
	}
	stagedHash, err := hashExecutable(stagedPath)
	if err != nil || stagedHash != r.executableHash {
		cleanup()
		return "", nil, errors.New("executable digest changed")
	}
	return stagedPath, cleanup, nil
}

func resolveCredentials(command Command, credentials *Credentials) (string, SignatureType, error) {
	if !command.requiresSecret {
		if credentials != nil && len(credentials.PrivateKey) != 0 {
			return "", "", &Error{Kind: ErrorInvalidCommand, Operation: command.operation, Message: "credentials are not accepted for this public command"}
		}
		return "", SignatureEOA, nil
	}
	if credentials == nil || len(credentials.PrivateKey) == 0 {
		return "", "", &Error{Kind: ErrorMissingSecret, Operation: command.operation, Message: "private key is required"}
	}
	secret, ok := normalizePrivateKey(credentials.PrivateKey)
	if !ok {
		return "", "", &Error{Kind: ErrorMissingSecret, Operation: command.operation, Message: "private key must be 32 raw bytes or 0x-prefixed 32-byte hexadecimal data"}
	}
	if !credentials.SignatureType.valid() {
		return "", "", &Error{Kind: ErrorInvalidCommand, Operation: command.operation, Message: "signature type must be eoa, proxy, or gnosis-safe"}
	}
	return secret, credentials.SignatureType, nil
}

func normalizePrivateKey(value []byte) (string, bool) {
	if len(value) == 32 {
		return "0x" + hex.EncodeToString(value), true
	}
	if len(value) != 66 || value[0] != '0' || value[1] != 'x' {
		return "", false
	}
	for _, char := range value[2:] {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') && !(char >= 'A' && char <= 'F') {
			return "", false
		}
	}
	return strings.ToLower(string(value)), true
}

func childEnvironment(secret string, signatureType SignatureType) []string {
	environment := make([]string, 0, len(inheritedEnvironment)+4)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found || protectedEnvironmentKey(key) {
			continue
		}
		if _, allowed := inheritedEnvironment[key]; allowed {
			environment = append(environment, entry)
		}
	}
	environment = append(environment,
		"POLYMARKET_CLOB_HOST="+ProductionCLOBHost,
		"POLYMARKET_RPC_URL="+ProductionRPCURL,
		"POLYMARKET_SIGNATURE_TYPE="+string(signatureType),
	)
	if secret != "" {
		environment = append(environment, "POLYMARKET_PRIVATE_KEY="+secret)
	}
	return environment
}

func protectedEnvironmentKey(key string) bool {
	for protected := range protectedEnvironment {
		if strings.EqualFold(key, protected) {
			return true
		}
	}
	return false
}

func classifyRunError(parent, run context.Context, operation string, runErr error, stderr, secret string) *Error {
	message := sanitizeErrorText(stderr, secret)
	if message == "" {
		message = "upstream command failed"
	}
	if errors.Is(parent.Err(), context.Canceled) {
		return &Error{Kind: ErrorCanceled, Operation: operation, Message: "upstream command was canceled"}
	}
	if errors.Is(parent.Err(), context.DeadlineExceeded) || errors.Is(run.Err(), context.DeadlineExceeded) {
		return &Error{Kind: ErrorTimeout, Operation: operation, Message: "upstream command timed out", Retryable: true}
	}
	var exitError *exec.ExitError
	if errors.As(runErr, &exitError) {
		return &Error{Kind: ErrorExecutionFailed, Operation: operation, ExitCode: exitError.ExitCode(), Message: message}
	}
	return &Error{Kind: ErrorStartFailed, Operation: operation, Message: sanitizeErrorText(runErr.Error(), secret)}
}

type boundedCapture struct {
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
	cancel   context.CancelFunc
}

func newBoundedCapture(limit int64, cancel context.CancelFunc) *boundedCapture {
	return &boundedCapture{limit: limit, cancel: cancel}
}

func (capture *boundedCapture) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := capture.limit - int64(capture.buffer.Len())
	if remaining > 0 {
		if int64(len(value)) > remaining {
			value = value[:remaining]
		}
		_, _ = capture.buffer.Write(value)
	}
	if int64(originalLength) > remaining {
		capture.exceeded = true
		capture.cancel()
	}
	return originalLength, nil
}
