package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPrivateKey = "0x1111111111111111111111111111111111111111111111111111111111111111"

func TestRunUsesFixedEnvironmentNoShellAndSanitizesJSON(t *testing.T) {
	t.Setenv("POLYMARKET_PRIVATE_KEY", "inherited-secret")
	t.Setenv("POLYMARKET_CLOB_HOST", "https://evil.invalid")
	t.Setenv("POLYMARKET_RPC_URL", "https://evil.invalid/rpc")
	t.Setenv("POLYMARKET_SIGNATURE_TYPE", "unknown")

	script := writeExecutable(t, `
if [ "$POLYMARKET_PRIVATE_KEY" != "`+testPrivateKey+`" ]; then
  printf 'wrong child private key' >&2
  exit 21
fi
if [ "$POLYMARKET_CLOB_HOST" != "https://clob.polymarket.com" ]; then exit 22; fi
if [ "$POLYMARKET_RPC_URL" != "https://polygon.drpc.org" ]; then exit 23; fi
if [ "$POLYMARKET_SIGNATURE_TYPE" != "proxy" ]; then exit 24; fi
case "$*" in *"`+testPrivateKey+`"*) exit 25 ;; esac
printf '{"host":"%s","rpc":"%s","signature_type":"%s","private_key":"%s","message":"secret=%s\\u001b","args":"%s"}\n' \
  "$POLYMARKET_CLOB_HOST" "$POLYMARKET_RPC_URL" "$POLYMARKET_SIGNATURE_TYPE" \
  "$POLYMARKET_PRIVATE_KEY" "$POLYMARKET_PRIVATE_KEY" "$*"
`)
	runner := mustRunner(t, script, Options{})
	command, err := BuildCancelOrder("order-1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), command, &Credentials{
		PrivateKey:    []byte(testPrivateKey),
		SignatureType: SignatureProxy,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(string(result.Raw), testPrivateKey) || strings.Contains(string(result.Raw), "inherited-secret") {
		t.Fatalf("result leaked private key: %s", result.Raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(result.Raw, &decoded); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if decoded["private_key"] != redacted || decoded["message"] != "secret=[REDACTED]" {
		t.Fatalf("credentials or controls were not sanitized: %#v", decoded)
	}
	if decoded["args"] != "--output json clob cancel order-1" {
		t.Fatalf("unexpected argv: %q", decoded["args"])
	}
}

func TestSanitizeJSONRedactsCaseTransformedSecret(t *testing.T) {
	secret := "0x" + strings.Repeat("ab", 32)
	upper := strings.ToUpper(secret)
	raw, err := sanitizeJSON([]byte(`{"value":"prefix-`+upper+`-suffix"}`), secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(secret)) || !strings.Contains(string(raw), redacted) {
		t.Fatalf("case-transformed secret was not redacted: %s", raw)
	}
}

func TestRunAcceptsRawPrivateKeyBytes(t *testing.T) {
	script := writeExecutable(t, `
if [ "$POLYMARKET_PRIVATE_KEY" != "`+testPrivateKey+`" ]; then exit 41; fi
printf '{"ok":true}'
`)
	runner := mustRunner(t, script, Options{})
	_, err := runner.Run(context.Background(), BuildCancelAll(), &Credentials{
		PrivateKey:    []byte(strings.Repeat("\x11", 32)),
		SignatureType: SignatureEOA,
	})
	if err != nil {
		t.Fatalf("Run() with raw private key error = %v", err)
	}
}

func TestRunPublicCommandDoesNotReceivePrivateKey(t *testing.T) {
	t.Setenv("POLYMARKET_PRIVATE_KEY", "inherited-secret")
	script := writeExecutable(t, `
if [ "${POLYMARKET_PRIVATE_KEY+x}" = "x" ]; then exit 31; fi
printf '{"host":"%s","rpc":"%s","signature_type":"%s"}\n' \
  "$POLYMARKET_CLOB_HOST" "$POLYMARKET_RPC_URL" "$POLYMARKET_SIGNATURE_TYPE"
`)
	runner := mustRunner(t, script, Options{})
	command, err := BuildApprovalCheck("0x" + strings.Repeat("12", 20))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := string(result.Raw); got != `{"host":"https://clob.polymarket.com","rpc":"https://polygon.drpc.org","signature_type":"eoa"}` {
		t.Fatalf("result = %s", got)
	}
}

func TestRunReturnsRedactedStructuredError(t *testing.T) {
	script := writeExecutable(t, `
printf 'private_key=%s api_key=leaky Authorization: Bearer very-secret\033[31m\n' "$POLYMARKET_PRIVATE_KEY" >&2
exit 9
`)
	runner := mustRunner(t, script, Options{})
	_, err := runner.Run(context.Background(), BuildCancelAll(), &Credentials{
		PrivateKey:    []byte(testPrivateKey),
		SignatureType: SignatureEOA,
	})
	var upstreamError *Error
	if !errors.As(err, &upstreamError) {
		t.Fatalf("Run() error = %T %v, want *Error", err, err)
	}
	if upstreamError.Kind != ErrorExecutionFailed || upstreamError.ExitCode != 9 {
		t.Fatalf("unexpected error: %#v", upstreamError)
	}
	serialized, _ := json.Marshal(upstreamError)
	for _, forbidden := range []string{testPrivateKey, "leaky", "very-secret", "\x1b"} {
		if strings.Contains(string(serialized), forbidden) || strings.Contains(upstreamError.Error(), forbidden) {
			t.Fatalf("error leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestRunEnforcesOutputLimits(t *testing.T) {
	script := writeExecutable(t, `
i=0
while [ "$i" -lt 1000 ]; do
  printf '1234567890'
  i=$((i + 1))
done
`)
	runner := mustRunner(t, script, Options{StdoutMaxBytes: 64})
	command, err := BuildApprovalCheck("0x" + strings.Repeat("12", 20))
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := runner.Run(context.Background(), command, nil)
	var upstreamError *Error
	if !errors.As(runErr, &upstreamError) || upstreamError.Kind != ErrorOutputTooLarge || upstreamError.Stream != "stdout" {
		t.Fatalf("Run() error = %#v, want stdout output_too_large", runErr)
	}
}

func TestRunHonorsContextCancellationAndTimeout(t *testing.T) {
	script := writeExecutable(t, `exec /bin/sleep 10`)
	command, err := BuildApprovalCheck("0x" + strings.Repeat("12", 20))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("canceled", func(t *testing.T) {
		runner := mustRunner(t, script, Options{Timeout: time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, runErr := runner.Run(ctx, command, nil)
		var upstreamError *Error
		if !errors.As(runErr, &upstreamError) || upstreamError.Kind != ErrorCanceled {
			t.Fatalf("Run() error = %#v, want canceled", runErr)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		runner := mustRunner(t, script, Options{Timeout: 10 * time.Millisecond})
		_, runErr := runner.Run(context.Background(), command, nil)
		var upstreamError *Error
		if !errors.As(runErr, &upstreamError) || upstreamError.Kind != ErrorTimeout || !upstreamError.Retryable {
			t.Fatalf("Run() error = %#v, want retryable timeout", runErr)
		}
	})
}

func TestRunRejectsInvalidJSONAndSecretMisuse(t *testing.T) {
	script := writeExecutable(t, `printf '{"ok":true} trailing'`)
	runner := mustRunner(t, script, Options{})
	public, err := BuildApprovalCheck("0x" + strings.Repeat("12", 20))
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := runner.Run(context.Background(), public, nil)
	var upstreamError *Error
	if !errors.As(runErr, &upstreamError) || upstreamError.Kind != ErrorInvalidJSON {
		t.Fatalf("Run() error = %#v, want invalid_json", runErr)
	}

	_, runErr = runner.Run(context.Background(), public, &Credentials{PrivateKey: []byte(testPrivateKey), SignatureType: SignatureEOA})
	if !errors.As(runErr, &upstreamError) || upstreamError.Kind != ErrorInvalidCommand {
		t.Fatalf("public command with credentials error = %#v", runErr)
	}

	_, runErr = runner.Run(context.Background(), BuildCancelAll(), nil)
	if !errors.As(runErr, &upstreamError) || upstreamError.Kind != ErrorMissingSecret {
		t.Fatalf("authenticated command without credentials error = %#v", runErr)
	}
}

func TestSanitizeJSONRejectsKeyCollisions(t *testing.T) {
	_, err := sanitizeJSON([]byte(`{"a\u0000":1,"a\\u0000":2}`), "")
	if err == nil {
		t.Fatal("sanitizeJSON() accepted keys that collide after sanitization")
	}
}

func TestNewRequiresExplicitExecutablePath(t *testing.T) {
	if _, err := New(Options{ExecutablePath: "polymarket"}); err == nil {
		t.Fatal("New() accepted PATH-resolved executable")
	}
	path := filepath.Join(t.TempDir(), "polymarket")
	if err := os.WriteFile(path, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{ExecutablePath: path}); err == nil {
		t.Fatal("New() accepted non-executable file")
	}
}

func TestRunRejectsExecutableIdentityChange(t *testing.T) {
	path := writeExecutable(t, `printf '{"ok":true}'`)
	runner := mustRunner(t, path, Options{})
	if !strings.HasPrefix(runner.ExecutableSHA256(), "sha256:") {
		t.Fatalf("missing executable digest: %q", runner.ExecutableSHA256())
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '{\"changed\":true}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := BuildApprovalCheck("0x" + strings.Repeat("12", 20))
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), command, nil)
	var upstreamError *Error
	if !errors.As(err, &upstreamError) || upstreamError.Kind != ErrorInvalidConfig {
		t.Fatalf("identity change returned %#v", err)
	}
}

func TestApprovalOperationsExposeAllUnlimitedTargets(t *testing.T) {
	operations := ApprovalOperations()
	if len(operations) != 11 {
		t.Fatalf("approval operation count=%d, want 11", len(operations))
	}
	erc20, erc1155 := 0, 0
	for index, operation := range operations {
		if operation.Step != index+1 || operation.TargetAddress == "" || operation.TokenContract == "" {
			t.Fatalf("incomplete approval operation: %#v", operation)
		}
		switch operation.Type {
		case "erc20-approve":
			erc20++
			if operation.Amount != maximumUint256 {
				t.Fatalf("non-max ERC20 approval: %#v", operation)
			}
		case "erc1155-set-approval-for-all":
			erc1155++
			if operation.Approved == nil || !*operation.Approved {
				t.Fatalf("disabled ERC1155 approval: %#v", operation)
			}
		default:
			t.Fatalf("unknown approval type: %#v", operation)
		}
	}
	if erc20 != 6 || erc1155 != 5 {
		t.Fatalf("approval types=%d/%d, want 6/5", erc20, erc1155)
	}
}

func TestInstallerRevisionMatchesRuntimeContract(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-polymarket-v2.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "revision='"+RequiredRevision+"'") {
		t.Fatalf("installer revision drifted from RequiredRevision %s", RequiredRevision)
	}
}

func writeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "polymarket")
	contents := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write helper executable: %v", err)
	}
	return path
}

func mustRunner(t *testing.T, path string, overrides Options) *Runner {
	t.Helper()
	overrides.ExecutablePath = path
	runner, err := New(overrides)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return runner
}
