# Agent contract

This document defines the stable behavioral contract for `pmx`. The runtime
registry exposed by `pmx schema <path>` is authoritative for command-specific
fields, defaults, effects, and limits.

## Discovery

`pmx schema` emits a deterministic command index without network, filesystem,
keychain, or signer access. Each leaf schema declares:

- strict request fields, types, enums, defaults, and limits
- authentication and profile requirements
- static effects and risk
- whether the command is agent-invocable
- dry-run and confirmation behavior
- output limits and stable examples

## Input and credential boundary

`--params <json>` and `--params -` accept exactly one bounded JSON object.
Duplicate keys, unknown properties, trailing values, wrong types, unsafe text,
and mixed convenience inputs are rejected before execution.

Credential-shaped keys such as `privateKey`, `mnemonic`, `seedPhrase`,
`apiKey`, and `secret` are rejected recursively. No `pmx` command defines a
private-key flag. Wallet import obtains key bytes only from masked input on the
controlling terminal, after the operation has been authorized.

Masked import is enabled only on targets where terminal input and restoration
can be bounded by the command context. Other targets fail closed before
reading a secret.

The private key is stored in the operating-system keychain. The public metadata
file is versioned, atomic, mode `0600`, and contains no secret. When an
authenticated upstream operation runs, the wallet manager releases raw bytes
only through a scoped callback; the caller-owned slice is zeroed when the
callback returns.

## Output

Command output is JSON. `--output json` makes that contract explicit; `--json`
remains a compatibility alias and cannot be combined with `--output`. Successful data is written only to stdout; error
envelopes are written only to stderr. Success uses `pmx.response/v1`, errors use
`pmx.error/v1`, and realized effects are always present under `meta.effects`.

`--fields` projects only `data`, so effects, operation status, pagination, and
truncation cannot be hidden. `--compact` changes whitespace only.

Provider output, decoded collection sizes, order-book levels, transaction
files, child-process stdout/stderr, and final JSON are independently bounded.
Provider and child-process text is sanitized for terminal controls,
directionality characters, unsafe zero-width characters, and credential-like
content.

## Mutation state machine

Every mutation supports two invocation states:

```text
validated request [+ --dry-run] -> dry-run plan
validated request + --execute -> controlling-TTY authorization -> protected action
```

Dry-run is the default. It sets `data.dryRun:true`, `data.executes:false`, and
`meta.effects.executed:false`. It must not:

- access a wallet secret or the keychain
- request terminal authorization or secret input
- generate a wallet key
- sign a message or order
- invoke the official execution engine
- send an HTTP mutation or RPC submission

The plan contains the normalized request, declared effects, relevant signed
transaction inspection, selected execution-engine path, and a SHA-256
fingerprint.

`--execute` is necessary but not sufficient. The same plan is printed to the
controlling terminal and an operator must type a phrase derived from its
fingerprint. Authorization is unavailable without a real TTY. There is no
`--yes`, plan-hash flag, or stdin-pipeline bypass.

Confirmation happens before keychain access, signing, wallet generation,
secret import, execution-engine invocation, or broadcast. Wallet import asks
for masked secret input only after this confirmation.

`clientRequestId` is a public reconciliation label. It does not make a provider
timeout safe to replay. If submission may have occurred but success cannot be
confirmed, `pmx` returns exit `9` and instructs the caller to reconcile.

## Wallet and signer scope

This release supports EOA profiles only. It intentionally does not infer or
accept unchecked numeric signature types for Polymarket Proxy, Gnosis Safe, or
Deposit Wallet accounts.

Wallet commands:

- `wallet.create`, `wallet.import`, `wallet.list`, `wallet.show`
- `wallet.use`, `wallet.remove`
- `wallet.sign-message` using EIP-191 personal-message semantics

Message signing is `agentInvocable:false`, mutation-classified, dry-run by
default, and operator-confirmed when live.

Live order creation is intentionally limited to GTC post-only limit orders.
Every request must include `maxNotionalPusd`; the plan publishes exact
price-times-size exposure and rejects a request above that caller-authorized
maximum before terminal, keychain, signing, or network access.

Authenticated order and trade lists preserve the official wrapper
`{data:[...],next_cursor:"..."}`. Their schema declares a 100-item hard bound,
and `meta.pagination` exposes the opaque next cursor unchanged. Only the exact
terminal cursor `LTE=` is complete.

## Trading execution engine

Authenticated CLOB operations use the current official Polymarket CLI through a
narrow process boundary:

- executable must be explicitly configured through `PMX_POLYMARKET_BIN`; `PATH`
  is not trusted for secret-bearing operations
- executable is an absolute, resolved, regular, non-writable-by-group/others
  file whose SHA-256 digest is captured and rechecked
- no shell and no arbitrary argv are available
- command builders allowlist exact operations and validate values
- CLOB host is fixed to `https://clob.polymarket.com`
- RPC is fixed to `https://polygon.drpc.org`
- signature type is fixed to EOA
- inherited Polymarket credentials, hosts, RPC values, proxies, and unrelated
  environment secrets are removed
- key material is supplied only in the child environment and never argv
- stdout and stderr are bounded and sanitized

Because this trusted child receives the key for one invocation, its resolved
path is included in affected mutation plans and terminal confirmations.
Readiness and plans also publish the required official source revision. The
packaged v0.1.x releases predate the CLOB V2 merge; the repository install
script pins the reviewed V2 revision instead.

The runner verifies and privately stages the same executable bytes before each
invocation, closing the path-replacement window between hashing and execution.
Plans still publish `executionEngineRevisionAttested:false`: a matching digest
detects replacement but does not prove source provenance.

## Signed transaction boundary

`transactions.inspect` accepts only an absolute private regular non-symlink
file with no group or other permissions and no larger than 256 KiB. It exposes
chain ID, sender, recipient, nonce, value, gas and fee caps, maximum execution
fee, type, calldata length, preview and selector, transaction hash, data hash,
and raw-file hash without broadcasting.

`transactions.submit` is `agentInvocable:false`. It accepts only an already
signed Polygon transaction, includes the decoded inspection in the plan,
requires the explicit `ARBITRARY_POLYGON_TRANSACTION` scope and operator
authorization, calls only the fixed Polygon RPC, verifies
`eth_chainId == 0x89`, and verifies the returned transaction hash. Raw signed
bytes are zeroed after use.

## Operator and trust boundary

The controlling-TTY phrase prevents accidental execution and stdin-pipeline
bypasses, but it is not cryptographic human-presence attestation. A process
that controls the same TTY can replay visible input. `agentInvocable:false` is
an advertised caller contract, not OS-level enforcement. Deployments must keep
the TTY, keychain session, wallet metadata directory, and
`PMX_POLYMARKET_BIN` configuration outside the control of untrusted agents.

The executable digest binds an operation to the configured bytes and detects
replacement after startup; it does not prove those bytes came from the pinned
source revision. Install from the pinned script and treat that binary as
trusted signer-adjacent code.

## Errors and exits

| Exit | Meaning | Automatic retry |
|---:|---|---|
| `0` | Complete success | Not applicable |
| `1` | Internal software failure | No |
| `2` | Invalid input or schema violation | No |
| `3` | Wallet, keychain, or authentication failure | No |
| `4` | Policy denial or missing authorization | No |
| `5` | Resource not found | No |
| `6` | Permanent provider rejection | No |
| `7` | Retryable read transport, timeout, or rate limit | Bounded backoff only |
| `8` | Partial result | Inspect first |
| `9` | Mutation result indeterminate | Never blindly replay |
| `130` | Interrupted | No |

## Fixed network boundary

| Service | Host | Purpose |
|---|---|---|
| Gamma | `https://gamma-api.polymarket.com` | Public markets and events |
| CLOB V2 | `https://clob.polymarket.com` | Public and authenticated trading |
| Polygon RPC | `https://polygon.drpc.org` | Approval and signed transaction submission |

Production chain ID is fixed to `137`.

## Design sources

- [Polymarket authentication](https://docs.polymarket.com/getting-started/api#authentication)
- [Wallet and signature types](https://docs.polymarket.com/trading/wallets-auth)
- [Place orders](https://docs.polymarket.com/trading/place-orders)
- [CLOB V2 migration](https://docs.polymarket.com/v2-migration)
- [Official Polymarket CLI](https://github.com/Polymarket/polymarket-cli)
- [Rewrite Your CLI for AI Agents](https://justin.poehnelt.com/posts/rewrite-your-cli-for-ai-agents/)
