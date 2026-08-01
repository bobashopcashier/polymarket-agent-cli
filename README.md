# pmx

`pmx` is an agent-first Polymarket CLI written in Go. It combines bounded
public market data with EOA wallet management, authenticated account reads,
post-only limit orders, cancellations, Polymarket V2 token approvals,
EIP-191 message signing, and signed Polygon transaction submission.

The private-key boundary is deliberate: no command accepts a private key,
mnemonic, API secret, or seed phrase in argv or `--params`. Wallet keys are
created or imported through a masked controlling-terminal prompt and stored in
the operating-system keychain. Live signing and financial mutations require a
separate typed confirmation on the controlling terminal.



## Why this CLI exists

Agents need a stricter contract than a human-oriented terminal program:

- JSON is the default in every environment.
- Every operation publishes an offline runtime schema.
- `--params` accepts one strict, API-shaped JSON object or bounded stdin.
- Output, provider payloads, and collections are bounded.
- Errors have stable codes, exit classes, and retryability signals.
- Provider strings are sanitized and always treated as untrusted data.
- Mutations dry-run by default and publish realized effects.
- `--execute` is not sufficient by itself; an operator must authorize the
  exact fingerprint from the controlling terminal.

The design follows [Justin Poehnelt's agent CLI guidance](https://justin.poehnelt.com/posts/rewrite-your-cli-for-ai-agents/), the [Polymarket CLOB V2 migration guide](https://docs.polymarket.com/v2-migration), and the [official Polymarket CLI](https://github.com/Polymarket/polymarket-cli).

## Install

Go 1.25 or newer is required.

The full masked wallet-import flow is supported on macOS and Unix-like
systems. On other targets it fails closed instead of starting an
uncancellable secret prompt.

```bash
make build
./dist/pmx --help
```

Authenticated CLOB and approval commands use the official Polymarket CLI as a
narrow execution engine. The latest packaged releases currently predate the
CLOB V2 merge, so do not use the Homebrew formula or v0.1.5 release for live
execution. Install the pinned official V2 revision with the included script:

```bash
./scripts/install-polymarket-v2.sh
```

This requires Rust 1.88 or newer and installs the `polymarket` binary through
Cargo. `pmx auth status` publishes the required official source revision.

Explicitly set `PMX_POLYMARKET_BIN` to the installed binary's absolute path;
`pmx` deliberately does not trust `PATH` for a secret-bearing child process:

```bash
export PMX_POLYMARKET_BIN="$HOME/.cargo/bin/polymarket"
```

The resolved executable path and SHA-256 digest are
included in every affected mutation plan and TTY confirmation. `pmx` invokes
only allowlisted argument vectors, fixes the CLOB host and Polygon RPC endpoint,
uses no shell, and supplies the key only in the short-lived child environment.
Treat the selected executable as trusted code because it receives the key for
that invocation.

Check readiness:

```bash
pmx auth status --compact
pmx doctor --compact
```

## Discover the contract

```bash
pmx schema
pmx schema orders.create
pmx schema wallet.import
pmx schema transactions.submit
```

Schemas are offline. They declare exact request fields, authentication,
effects, risk, output limits, whether an agent may invoke the command, and
whether a controlling-terminal confirmation is required.

Prefer strict JSON requests:

```bash
pmx markets search --params '{"q":"bitcoin","limit":5}'

printf '%s\n' '{"active":true,"closed":false,"limit":10}' | \
  pmx markets list --params - --compact
```

Unknown fields, duplicate keys, wrong types, credential-shaped keys, unsafe
controls, and oversized inputs fail before network or keychain access.

## Wallets

Generate a wallet:

```bash
pmx wallet create --params '{"name":"trading"}'
pmx wallet create --execute --params '{"name":"trading"}'
```

The first command returns a deterministic plan and performs no key generation,
keychain access, or write. The second displays the plan on `/dev/tty`, requires
a one-time phrase, generates the key, and stores it in the OS keychain.

Import requires an expected public address. The private key is requested only
after authorization through masked terminal input:

```bash
pmx wallet import --execute \
  --params '{"name":"existing","expectedAddress":"0xYourExpectedAddress"}'
```

There is intentionally no `--private-key` flag and no private-key environment
variable consumed by `pmx`.

```bash
pmx wallet list
pmx wallet show --params '{}'
pmx wallet use --execute --params '{"name":"existing"}'
pmx wallet remove --execute --params '{"name":"existing"}'
```

Wallet metadata contains only name, checksummed address, signature type, and
optional funder. It is written atomically with mode `0600`; key material is not
written to that file.

## Trading and account commands

Authenticated reads use the active wallet unless `wallet` is supplied:

```bash
pmx orders list --params '{}'
pmx orders get --params '{"id":"0xOrderId"}'
pmx trades list --params '{}'
pmx balances get --params '{"assetType":"collateral"}'
pmx approvals check --params '{}'
```

Every mutation plans by default:

```bash
pmx orders create --params '{
  "tokenId":"1234567890",
  "side":"BUY",
  "price":"0.45",
  "size":"10",
  "maxNotionalPusd":"5",
  "clientRequestId":"order-20260731-001"
}'
```

The response contains `dryRun:true`, `executes:false`, the normalized request,
computed price-times-size exposure, caller-authorized maximum, effects,
selected execution engine, and a SHA-256 fingerprint. Orders are always GTC
and post-only in this release. To execute the same request, add `--execute` and
authorize its newly displayed fingerprint on the controlling terminal:

```bash
pmx orders create --execute --params '{
  "tokenId":"1234567890",
  "side":"BUY",
  "price":"0.45",
  "size":"10",
  "maxNotionalPusd":"5",
  "clientRequestId":"order-20260731-001"
}'
```

Supported mutations:

| Command | Purpose | Agent invocable |
|---|---|---:|
| `orders.create` | GTC post-only limit order within `maxNotionalPusd` | Yes, but live execution still needs TTY authorization |
| `orders.cancel` | Cancel one order | Yes, with TTY authorization |
| `orders.cancel-batch` | Cancel up to 100 orders | Yes, with TTY authorization |
| `orders.cancel-market` | Cancel by condition or token | Yes, with TTY authorization |
| `orders.cancel-all` | Cancel every open order | No |
| `approvals.set` | Grant the official V2 trading approvals | No |

`approvals.set` uses the official CLI's broad protocol approval flow and is
classified critical. Its plan lists all 11 sequential approval transactions:
six unlimited, max-uint256 pUSD ERC-20 allowances and five conditional-token
ERC-1155 operator approvals. The plan identifies the source revision and says
that the configured binary's revision is not attested. Partial completion is
possible before exit `9`; reconcile every target before retrying.

`clientRequestId` is a caller reconciliation key. It does not make an uncertain
mutation safe to retry. If a mutation returns exit `9`, inspect open orders,
allowances, or the transaction hash before taking another action.

## Signing and transactions

EIP-191 personal-message signing is operator-restricted by the published
contract:

```bash
pmx wallet sign-message --execute --params '{
  "message":"example.com login nonce 123",
  "clientRequestId":"sign-001"
}'
```

Signed transaction files can be inspected without a network call. The path
must be absolute, regular, non-symlinked, private (no group or other
permissions), and no larger than 256 KiB:

```bash
pmx transactions inspect \
  --params '{"rawTransactionFile":"/secure/path/transaction.hex"}'
```

Submission accepts only an already-signed Polygon transaction and publishes
`agentInvocable:false`. `pmx` displays chain ID, sender, recipient, value,
maximum execution fee, gas-fee fields, calldata preview, selector, and hashes
before authorization, then attests the fixed RPC endpoint's chain ID before
broadcasting:

```bash
pmx transactions submit --execute --params '{
  "rawTransactionFile":"/secure/path/transaction.hex",
  "scope":"ARBITRARY_POLYGON_TRANSACTION",
  "clientRequestId":"tx-001"
}'
```

## Public data

Public commands remain wallet-free:

```bash
pmx markets list --params '{"active":true,"closed":false,"limit":10}'
pmx markets get --params '{"id":"example-market-slug"}'
pmx markets search --params '{"q":"bitcoin","limit":5}'
pmx events list --params '{"tag":"politics","limit":10}'
pmx clob price --params '{"tokenId":"1234567890","side":"BUY"}'
pmx clob book --params '{"tokenId":"1234567890"}' \
  --fields market,asset_id,timestamp,bids,asks --compact
```

## Output and exits

Successful output uses `pmx.response/v1`; errors use `pmx.error/v1` on stderr.
`--fields` projects only `data`, preserving safety metadata. `--compact`
changes whitespace only.

| Exit | Meaning |
|---:|---|
| `0` | Complete success |
| `1` | Internal software error |
| `2` | Invalid input or schema violation |
| `3` | Authentication, wallet, or keychain failure |
| `4` | Policy denial or missing operator authorization |
| `5` | Resource not found |
| `6` | Permanent provider rejection |
| `7` | Retryable transport, timeout, or rate limit |
| `8` | Partial result |
| `9` | Indeterminate mutation; reconcile and do not blindly retry |
| `130` | Interrupted |

## Security boundary

- Public hosts are fixed to Gamma and the production CLOB.
- Trading is fixed to Polymarket CLOB V2 on Polygon chain ID `137`.
- The transaction RPC is fixed to `https://polygon.drpc.org` and must attest
  chain ID `137` before submission.
- Private keys are forbidden in argv and `--params`; inherited Polymarket
  credential variables are removed before starting the execution engine.
- Live mutations require `--execute` plus a controlling-terminal phrase. There
  is no `--yes` bypass.
- Generic message signing and raw transaction submission publish
  `agentInvocable:false`.
- Provider failures after a mutation may be classified indeterminate rather
  than retried.
- The TTY phrase is an operator-interaction gate, not a cryptographic proof of
  human presence: software controlling the same TTY could replay it. Likewise,
  `agentInvocable:false` is a published caller contract, not OS-level identity
  enforcement. Do not give an untrusted agent access to the controlling TTY,
  OS keychain, or `PMX_POLYMARKET_BIN` configuration.
- This project is independent and is not an official Polymarket product.

See [CONTEXT.md](CONTEXT.md) for concise agent rules and
[docs/contract.md](docs/contract.md) for the detailed interface contract.

## Verification

```bash
make check
```

This runs formatting checks, `go vet`, normal tests, race tests, and a clean
build.

## Sources

- [Polymarket authentication](https://docs.polymarket.com/getting-started/api#authentication)
- [Polymarket wallet and signature types](https://docs.polymarket.com/trading/wallets-auth)
- [Polymarket order placement](https://docs.polymarket.com/trading/place-orders)
- [Polymarket CLOB V2 migration](https://docs.polymarket.com/v2-migration)
- [Polymarket contracts](https://docs.polymarket.com/resources/contracts)
- [Official Polymarket CLI](https://github.com/Polymarket/polymarket-cli)
- [Rewrite Your CLI for AI Agents](https://justin.poehnelt.com/posts/rewrite-your-cli-for-ai-agents/)

## License

[MIT](LICENSE)
