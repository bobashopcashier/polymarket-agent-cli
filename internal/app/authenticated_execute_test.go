package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/transaction"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/upstream"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/wallet"
)

type trackingSecrets struct {
	mu      sync.Mutex
	values  map[string][]byte
	gets    int
	sets    int
	deletes int
}

func newTrackingSecrets() *trackingSecrets { return &trackingSecrets{values: map[string][]byte{}} }

func (s *trackingSecrets) Set(_ context.Context, id string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets++
	s.values[id] = append([]byte(nil), value...)
	return nil
}

func (s *trackingSecrets) Get(_ context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	value, ok := s.values[id]
	if !ok {
		return nil, wallet.ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *trackingSecrets) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	if _, ok := s.values[id]; !ok {
		return wallet.ErrSecretNotFound
	}
	delete(s.values, id)
	return nil
}

func (s *trackingSecrets) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets, s.sets, s.deletes
}

type fakeConsole struct {
	confirmErr  error
	secret      []byte
	confirms    int
	secretReads int
	lastSummary string
	lastPhrase  string
}

type blockingConsole struct{}

func (blockingConsole) Confirm(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingConsole) ReadSecret(ctx context.Context, _ string, _ int) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *fakeConsole) Confirm(_ context.Context, summary, phrase string) error {
	c.confirms++
	c.lastSummary = summary
	c.lastPhrase = phrase
	return c.confirmErr
}

func (c *fakeConsole) ReadSecret(_ context.Context, _ string, _ int) ([]byte, error) {
	c.secretReads++
	if len(c.secret) == 0 {
		return nil, errors.New("no test secret")
	}
	return append([]byte(nil), c.secret...), nil
}

func testAuthenticatedApp(t *testing.T, terminal *fakeConsole, runner *upstream.Runner) (*App, *bytes.Buffer, *bytes.Buffer, *trackingSecrets) {
	t.Helper()
	secrets := newTrackingSecrets()
	manager, err := wallet.NewManager(filepath.Join(t.TempDir(), "wallets.json"), secrets)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	application, err := New(Dependencies{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Console: terminal, Wallets: manager, Upstream: runner,
		TxSender: &transaction.Sender{Client: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	return application, &stdout, &stderr, secrets
}

func testUpstreamRunner(t *testing.T, body string) *upstream.Runner {
	t.Helper()
	path := filepath.Join(t.TempDir(), "polymarket")
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner, err := upstream.New(upstream.Options{ExecutablePath: path})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestMutationDryRunDoesNotAuthorizeReadSecretsOrExecute(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	runner := testUpstreamRunner(t, `touch "`+marker+`"; printf '{"unexpected":true}'`)
	terminal := &fakeConsole{}
	application, stdout, stderr, secrets := testAuthenticatedApp(t, terminal, runner)
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	getsBefore, setsBefore, deletesBefore := secrets.counts()
	exit := application.Run(context.Background(), []string{
		"orders", "create", "--params", `{"tokenId":"123","side":"BUY","price":"0.45","size":"10","maxNotionalPusd":"5","clientRequestId":"dry-1"}`, "--compact",
	})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	gets, sets, deletes := secrets.counts()
	if terminal.confirms != 0 || terminal.secretReads != 0 || gets != getsBefore || sets != setsBefore || deletes != deletesBefore {
		t.Fatalf("dry-run crossed a protected boundary: terminal=%#v counts=%d/%d/%d", terminal, gets, sets, deletes)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("upstream was executed during dry-run: %v", err)
	}
	document := decodeDocument(t, stdout)
	data := document["data"].(map[string]any)
	if data["dryRun"] != true || data["executes"] != false {
		t.Fatalf("unexpected dry-run response: %#v", data)
	}
	plan := data["plan"].(map[string]any)
	exposure := plan["orderExposure"].(map[string]any)
	if exposure["computedNotional"] != "4.5" || exposure["callerMaximum"] != "5" || exposure["postOnly"] != true {
		t.Fatalf("unexpected order exposure: %#v", exposure)
	}
	effects := document["meta"].(map[string]any)["effects"].(map[string]any)
	if effects["executed"] != false || effects["mutation"] != "order.create" {
		t.Fatalf("unexpected effects: %#v", effects)
	}
}

func TestConfirmedOrderUsesKeychainSecretOnlyInChildEnvironment(t *testing.T) {
	runner := testUpstreamRunner(t, `
if [ "${#POLYMARKET_PRIVATE_KEY}" -ne 66 ]; then exit 42; fi
case "$*" in *"$POLYMARKET_PRIVATE_KEY"*) exit 43 ;; esac
printf '{"orderID":"order-1","status":"live","args":"%s"}' "$*"
`)
	terminal := &fakeConsole{}
	application, stdout, stderr, secrets := testAuthenticatedApp(t, terminal, runner)
	entry, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"})
	if err != nil {
		t.Fatal(err)
	}
	exit := application.Run(context.Background(), []string{
		"orders", "create", "--execute", "--params", `{"wallet":"trading","tokenId":"123","side":"BUY","price":"0.45","size":"10","maxNotionalPusd":"5","clientRequestId":"live-1"}`, "--compact",
	})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if terminal.confirms != 1 || terminal.secretReads != 0 || !strings.HasPrefix(terminal.lastPhrase, "execute-") {
		t.Fatalf("unexpected authorization calls: %#v", terminal)
	}
	if !strings.Contains(terminal.lastSummary, `"command": "orders.create"`) || strings.Contains(terminal.lastSummary, entry.Address+"private") {
		t.Fatalf("unexpected authorization summary: %s", terminal.lastSummary)
	}
	gets, _, _ := secrets.counts()
	if gets != 1 {
		t.Fatalf("wallet secret reads=%d, want 1", gets)
	}
	document := decodeDocument(t, stdout)
	data := document["data"].(map[string]any)["result"].(map[string]any)
	if data["orderID"] != "order-1" || !strings.Contains(data["args"].(string), "clob create-order") {
		t.Fatalf("unexpected upstream result: %#v", data)
	}
}

func TestOrderNotionalMustStayWithinCallerMaximum(t *testing.T) {
	terminal := &fakeConsole{}
	application, stdout, stderr, _ := testAuthenticatedApp(t, terminal, testUpstreamRunner(t, `printf '{"unexpected":true}'`))
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	exit := application.Run(context.Background(), []string{
		"orders", "create", "--params", `{"tokenId":"123","side":"BUY","price":"0.45","size":"10","maxNotionalPusd":"4","clientRequestId":"limit-1"}`, "--compact",
	})
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"order_notional_exceeded"`) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if terminal.confirms != 0 || terminal.secretReads != 0 {
		t.Fatalf("exposure rejection crossed a protected boundary: %#v", terminal)
	}
	stderr.Reset()
	exit = application.Run(context.Background(), []string{
		"orders", "create", "--params", `{"tokenId":"123","side":"BUY","price":"0","size":"10","maxNotionalPusd":"1","clientRequestId":"zero-1"}`, "--compact",
	})
	if exit != 2 || !strings.Contains(stderr.String(), `"code":"invalid_parameter_value"`) {
		t.Fatalf("zero price exit=%d stderr=%s", exit, stderr.String())
	}
}

func TestDeniedConfirmationStopsBeforeSecretAndUpstream(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	runner := testUpstreamRunner(t, `touch "`+marker+`"; printf '{"ok":true}'`)
	terminal := &fakeConsole{confirmErr: errors.New("denied")}
	application, stdout, stderr, secrets := testAuthenticatedApp(t, terminal, runner)
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	getsBefore, _, _ := secrets.counts()
	exit := application.Run(context.Background(), []string{
		"orders", "cancel", "--execute", "--params", `{"orderId":"0x` + strings.Repeat("1", 64) + `","clientRequestId":"deny-1"}`, "--compact",
	})
	if exit != 4 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	getsAfter, _, _ := secrets.counts()
	if getsAfter != getsBefore {
		t.Fatalf("secret was read before authorization: before=%d after=%d", getsBefore, getsAfter)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("upstream ran after denied authorization: %v", err)
	}
}

func TestLiveUpstreamMutationRejectsMissingEngineBeforeAuthorization(t *testing.T) {
	t.Setenv("PMX_POLYMARKET_BIN", "")
	terminal := &fakeConsole{}
	application, stdout, stderr, secrets := testAuthenticatedApp(t, terminal, nil)
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	getsBefore, _, _ := secrets.counts()
	exit := application.Run(context.Background(), []string{
		"orders", "cancel", "--execute", "--params", `{"orderId":"0x` + strings.Repeat("1", 64) + `","clientRequestId":"no-engine-1"}`, "--compact",
	})
	if exit != 4 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"upstream_not_configured"`) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	getsAfter, _, _ := secrets.counts()
	if terminal.confirms != 0 || terminal.secretReads != 0 || getsAfter != getsBefore {
		t.Fatalf("missing engine crossed a protected boundary: terminal=%#v secret reads=%d/%d", terminal, getsBefore, getsAfter)
	}
}

func TestMutationFieldProjectionUsesStableWrapperPaths(t *testing.T) {
	terminal := &fakeConsole{}
	application, stdout, stderr, _ := testAuthenticatedApp(t, terminal, testUpstreamRunner(t, `printf '{"ok":true}'`))

	exit := application.Run(context.Background(), []string{
		"wallet", "create", "--params", `{"name":"projected"}`, "--fields", "plan", "--compact",
	})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("dry projection exit=%d stderr=%s", exit, stderr.String())
	}
	dry := decodeDocument(t, stdout)["data"].(map[string]any)
	if _, ok := dry["plan"]; !ok || len(dry) != 1 {
		t.Fatalf("unexpected dry projection: %#v", dry)
	}

	stdout.Reset()
	stderr.Reset()
	exit = application.Run(context.Background(), []string{
		"wallet", "create", "--execute", "--params", `{"name":"projected"}`, "--fields", "result.name", "--compact",
	})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("live projection exit=%d stderr=%s", exit, stderr.String())
	}
	live := decodeDocument(t, stdout)["data"].(map[string]any)
	result := live["result"].(map[string]any)
	if result["name"] != "projected" || len(result) != 1 || len(live) != 1 {
		t.Fatalf("unexpected live projection: %#v", live)
	}
}

func TestWalletImportReadsPrivateKeyOnlyFromConsole(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := ethcrypto.FromECDSA(key)
	defer zeroBytes(secret)
	address := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
	terminal := &fakeConsole{secret: secret}
	application, stdout, stderr, _ := testAuthenticatedApp(t, terminal, nil)

	params := `{"name":"imported","expectedAddress":"` + address + `"}`
	if exit := application.Run(context.Background(), []string{"wallet", "import", "--params", params, "--compact"}); exit != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", exit, stderr.String())
	}
	if terminal.confirms != 0 || terminal.secretReads != 0 {
		t.Fatalf("dry-run requested secret: %#v", terminal)
	}
	stdout.Reset()
	if exit := application.Run(context.Background(), []string{"wallet", "import", "--execute", "--params", params, "--compact"}); exit != 0 {
		t.Fatalf("execute exit=%d stderr=%s", exit, stderr.String())
	}
	if terminal.confirms != 1 || terminal.secretReads != 1 {
		t.Fatalf("unexpected console interactions: %#v", terminal)
	}
	if strings.Contains(stdout.String(), hexKey(secret)) || strings.Contains(stderr.String(), hexKey(secret)) {
		t.Fatal("private key appeared in command output")
	}
	document := decodeDocument(t, stdout)
	if document["data"].(map[string]any)["result"].(map[string]any)["address"] != address {
		t.Fatalf("unexpected import output: %#v", document)
	}
}

func TestPrivateKeyParametersAreAlwaysRejected(t *testing.T) {
	application, stdout, stderr, _ := testAuthenticatedApp(t, &fakeConsole{}, nil)
	secret := "0x" + strings.Repeat("1", 64)
	for _, arguments := range [][]string{
		{"wallet", "import", "--params", `{"name":"x","expectedAddress":"0x0000000000000000000000000000000000000000","privateKey":"` + secret + `"}`},
		{"wallet", "import", "--name", "x", "--expected-address", "0x0000000000000000000000000000000000000000", "--private-key", secret},
	} {
		stdout.Reset()
		stderr.Reset()
		if exit := application.Run(context.Background(), arguments); exit != 2 {
			t.Fatalf("arguments=%v exit=%d stderr=%s", arguments[:2], exit, stderr.String())
		}
		if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
			t.Fatal("rejected private key was echoed")
		}
	}
}

func TestMessageSigningUsesManagedWalletAndEIP191(t *testing.T) {
	terminal := &fakeConsole{}
	application, stdout, stderr, _ := testAuthenticatedApp(t, terminal, nil)
	entry, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "signer"})
	if err != nil {
		t.Fatal(err)
	}
	if exit := application.Run(context.Background(), []string{
		"wallet", "sign-message", "--execute", "--params", `{"wallet":"signer","message":"login nonce 123","clientRequestId":"sign-1"}`, "--compact",
	}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	document := decodeDocument(t, stdout)
	data := document["data"].(map[string]any)["result"].(map[string]any)
	signature, err := hex.DecodeString(strings.TrimPrefix(data["signature"].(string), "0x"))
	if err != nil || len(signature) != 65 {
		t.Fatalf("invalid signature output: %v, %x", err, signature)
	}
	signature[64] -= 27
	digest, err := hex.DecodeString(strings.TrimPrefix(data["messageHash"].(string), "0x"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ethcrypto.SigToPub(digest, signature)
	if err != nil {
		t.Fatal(err)
	}
	if recovered := ethcrypto.PubkeyToAddress(*publicKey).Hex(); recovered != entry.Address {
		t.Fatalf("signature recovered %s, want %s", recovered, entry.Address)
	}
}

func TestTransactionSubmissionRejectsWrongChainBeforeAuthorization(t *testing.T) {
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.LegacyTx{Nonce: 1, To: ptrAddress(common.HexToAddress("0x0000000000000000000000000000000000000001")), Gas: 21_000, GasPrice: big.NewInt(1), Value: big.NewInt(1)})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "transaction.bin")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	terminal := &fakeConsole{}
	application, stdout, stderr, _ := testAuthenticatedApp(t, terminal, nil)
	exit := application.Run(context.Background(), []string{
		"transactions", "submit", "--execute", "--params", `{"rawTransactionFile":"` + path + `","scope":"ARBITRARY_POLYGON_TRANSACTION","clientRequestId":"tx-1"}`, "--compact",
	})
	if exit != 2 || stdout.Len() != 0 || terminal.confirms != 0 {
		t.Fatalf("exit=%d confirms=%d stdout=%s stderr=%s", exit, terminal.confirms, stdout.String(), stderr.String())
	}
}

func TestExplicitWalletSelectionMatchesSchemaForShowAndApprovalCheck(t *testing.T) {
	runner := testUpstreamRunner(t, `
case "$*" in
  *"approve check"*) printf '[{"contract":"CTF Exchange","approved":true,"args":"%s"}]' "$*" ;;
  *) exit 44 ;;
esac
`)
	application, stdout, stderr, _ := testAuthenticatedApp(t, &fakeConsole{}, runner)
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	beta, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if exit := application.Run(context.Background(), []string{"wallet", "show", "--params", `{"wallet":"beta"}`, "--compact"}); exit != 0 {
		t.Fatalf("wallet show exit=%d stderr=%s", exit, stderr.String())
	}
	document := decodeDocument(t, stdout)
	if document["data"].(map[string]any)["name"] != "beta" {
		t.Fatalf("explicit wallet was ignored: %#v", document)
	}
	stdout.Reset()
	if exit := application.Run(context.Background(), []string{"approvals", "check", "--params", `{"wallet":"beta"}`, "--compact"}); exit != 0 {
		t.Fatalf("approval check exit=%d stderr=%s", exit, stderr.String())
	}
	document = decodeDocument(t, stdout)
	data := document["data"].([]any)
	if len(data) != 1 || !strings.Contains(data[0].(map[string]any)["args"].(string), beta.Address) {
		t.Fatalf("approval check used wrong wallet: %#v", data)
	}
}

func TestAuthStatusChecksKeychainSecretHealth(t *testing.T) {
	runner := testUpstreamRunner(t, `printf '{"ok":true}'`)
	application, stdout, stderr, secrets := testAuthenticatedApp(t, &fakeConsole{}, runner)
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Delete(context.Background(), "wallet/trading"); err != nil {
		t.Fatal(err)
	}
	if exit := application.Run(context.Background(), []string{"auth", "status", "--compact"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	data := decodeDocument(t, stdout)["data"].(map[string]any)
	if data["walletConfigured"] != true || data["walletSecretReady"] != false || data["ready"] != false || data["walletError"] != "secret_not_found" {
		t.Fatalf("readiness ignored missing secret: %#v", data)
	}
}

func TestManagedMutationTimeoutIncludesTTYWait(t *testing.T) {
	secrets := newTrackingSecrets()
	manager, err := wallet.NewManager(filepath.Join(t.TempDir(), "wallets.json"), secrets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	runner := testUpstreamRunner(t, `printf '{"unexpected":true}'`)
	var stdout, stderr bytes.Buffer
	application, err := New(Dependencies{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Console: blockingConsole{}, Wallets: manager, Upstream: runner})
	if err != nil {
		t.Fatal(err)
	}
	getsBefore, _, _ := secrets.counts()
	exit := application.Run(context.Background(), []string{
		"orders", "cancel", "--execute", "--timeout", "10ms", "--params", `{"orderId":"0x` + strings.Repeat("1", 64) + `","clientRequestId":"timeout-1"}`, "--compact",
	})
	if exit != 7 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	getsAfter, _, _ := secrets.counts()
	if getsAfter != getsBefore {
		t.Fatalf("secret was read while waiting for authorization: %d -> %d", getsBefore, getsAfter)
	}
}

func TestTimeoutCoversParamsStdin(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	manager, err := wallet.NewManager(filepath.Join(t.TempDir(), "wallets.json"), newTrackingSecrets())
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	application, err := New(Dependencies{Stdin: reader, Stdout: &stdout, Stderr: &stderr, Console: &fakeConsole{}, Wallets: manager})
	if err != nil {
		t.Fatal(err)
	}
	exit := application.Run(context.Background(), []string{"markets", "list", "--params", "-", "--timeout", "5ms", "--compact"})
	if exit != 7 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"timeout"`) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
}

func TestAuthenticatedCursorPaginationAndSchema(t *testing.T) {
	runner := testUpstreamRunner(t, `printf '{"data":[{"id":"order-1"}],"next_cursor":"next-token=="}'`)
	application, stdout, stderr, _ := testAuthenticatedApp(t, &fakeConsole{}, runner)
	if _, err := application.wallets.Generate(context.Background(), wallet.GenerateOptions{Name: "trading"}); err != nil {
		t.Fatal(err)
	}
	exit := application.Run(context.Background(), []string{"orders", "list", "--params", `{}`, "--compact"})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	document := decodeDocument(t, stdout)
	pagination := document["meta"].(map[string]any)["pagination"].(map[string]any)
	if pagination["itemsEmitted"] != json.Number("1") || pagination["pagesFetched"] != json.Number("1") || pagination["complete"] != false || pagination["nextCursor"] != "next-token==" {
		t.Fatalf("unexpected pagination: %#v", pagination)
	}
	spec, ok := application.commands.Lookup("orders", "list")
	if !ok || !spec.Output.Collection || spec.Pagination == nil || spec.Pagination.CursorField != "cursor" || spec.Output.HardItemLimit != 100 {
		t.Fatalf("orders.list schema does not publish bounded cursor pagination: %#v", spec)
	}
}

func TestAuthenticatedTerminalCursorAndWrappedCollectionBound(t *testing.T) {
	application, _, _, _ := testAuthenticatedApp(t, &fakeConsole{}, testUpstreamRunner(t, `printf '{"ok":true}'`))
	spec, ok := application.commands.Lookup("trades", "list")
	if !ok {
		t.Fatal("trades.list is missing")
	}
	items := make([]any, 101)
	for index := range items {
		items[index] = map[string]any{"id": index}
	}
	bounded, truncation := boundResult(spec, map[string]any{"data": items, "next_cursor": "LTE="})
	if collectionCount(bounded) != 100 || len(truncation) != 1 || authenticatedPagination(bounded, true).Complete {
		t.Fatalf("wrapped collection was not safely bounded: count=%d truncation=%#v", collectionCount(bounded), truncation)
	}
	if !authenticatedPagination(map[string]any{"data": []any{}, "next_cursor": "LTE="}, false).Complete {
		t.Fatal("terminal CLOB cursor was not recognized")
	}
}

func TestCriticalMutationSchemaMatchesRuntimeBoundaries(t *testing.T) {
	application, _, _, _ := testAuthenticatedApp(t, &fakeConsole{}, testUpstreamRunner(t, `printf '{"ok":true}'`))
	transactionSpec, ok := application.commands.Lookup("transactions", "submit")
	if !ok || transactionSpec.Auth.Mode != registry.AuthNone || transactionSpec.Auth.ProfileRequired {
		t.Fatalf("transactions.submit incorrectly requires a wallet: %#v", transactionSpec.Auth)
	}
	orderSpec, ok := application.commands.Lookup("orders", "create")
	if !ok || orderSpec.Effects.Effects.Reversible {
		t.Fatalf("orders.create reversibility is unsafe: %#v", orderSpec.Effects.Effects)
	}
}

func ptrAddress(value common.Address) *common.Address { return &value }

func hexKey(secret []byte) string { return "0x" + fmtHex(secret) }

func fmtHex(value []byte) string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, current := range value {
		result[index*2] = alphabet[current>>4]
		result[index*2+1] = alphabet[current&15]
	}
	return string(result)
}
