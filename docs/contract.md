# Agent contract

This document defines the stable behavioral contract for `pmx`. Command-specific
types, defaults, limits, and examples are authoritative at runtime through
`pmx schema <path>`.

## Discovery

`pmx schema` emits a compact, deterministic index without network access.
`pmx schema markets.list` and `pmx schema markets list` address the same leaf.
Every leaf schema declares:

- request fields and JSON types
- required values, enums, defaults, and numeric or byte limits
- whether the operation reads the network or mutates external state
- output formats, byte limits, and item limits
- stable error codes
- examples

Help and schema operations must not access credentials or public providers.

## Strict request JSON

`--params <json>` and `--params -` provide the raw request path. The parser
accepts exactly one JSON object and rejects:

- duplicate or unknown keys
- a second JSON value or trailing non-whitespace
- a scalar or array at the root
- wrong JSON types
- values outside the published range
- controls, directionality characters, embedded query strings, fragments, and
  pre-encoded traversal in resource identifiers
- payloads above the published byte limit
- any mixture with request flags or positional arguments

Global output controls remain outside the request object. This prevents a
provider-shaped request from changing rendering or policy.

## Output

Command results always use JSON; `--json` is an explicit compatibility flag.
Successful machine output is written only to stdout. Machine errors are written
only to stderr. Logs and progress must never contaminate stdout.

Success documents use `pmx.response/v1` and contain `ok`, `command`, `data`,
and `meta`. Metadata carries realized effects plus any pagination and truncation
state. Field projection applies only to `data`, so safety metadata cannot be
hidden by a field mask.

`--fields` accepts comma-separated dotted paths and validates them against the
declared response type, including when a response array is empty. `--compact`
changes whitespace only. Neither option changes the provider request.

Provider payload bytes, decoded collection sizes, nested order-book levels, and
final JSON bytes are independently bounded. A shortened collection carries
truncation metadata containing the affected path and source and emitted counts.
An oversized final document fails before partial JSON is written.

Provider text is sanitized for ANSI terminal controls, C0 and C1 controls,
bidirectional overrides and isolates, and unsafe zero-width characters. This
sanitization does not make provider content trustworthy.

## Errors and exits

The JSON error envelope is `pmx.error/v1`. Its stable fields are `code`,
`category`, `message`, `exitCode`, and `retryable`. Optional details must be
sanitized and must never contain credentials.

| Exit | Meaning | Retry automatically? |
|---:|---|---|
| `0` | Complete success | Not applicable |
| `1` | Internal software error | No |
| `2` | Invalid arguments or schema violation | No |
| `3` | Configuration or authentication failure | No |
| `4` | Policy denial or missing confirmation | No |
| `5` | Resource not found | No |
| `6` | Permanent provider rejection | No |
| `7` | Retryable transport, timeout, or rate limit | Only with bounded backoff |
| `8` | Partial result | Inspect before continuing |
| `9` | Indeterminate result | Reconcile, never blindly replay |
| `130` | Interrupted by caller | No automatic replay |

The MVP is read-only, so mutation-specific classes are reserved for compatible
future expansion.

## Network boundary

The public providers are fixed:

| Service | Host | Purpose |
|---|---|---|
| Gamma API | `https://gamma-api.polymarket.com` | Markets, events, and discovery |
| CLOB API | `https://clob.polymarket.com` | Public prices and order-book data |

Requests use bounded timeouts, response-size limits, a stable user agent, and
strict URL construction. User values never become an arbitrary URL or host.

## Mutation boundary

The MVP registers no mutation operation and reads no wallet credential. No
order placement, cancellation, token approval, signing, or transaction
submission is available.

Future mutation work must first expose a dry-run plan that:

- sets `executes` to `false`
- performs no HTTP mutation, signing, credential read, journal write, or chain
  submission
- normalizes the intended request and enumerates effects
- states policy decisions and maximum exposure
- contains no private material
- is deterministic for the same request and policy snapshot

A dry-run plan is not authorization to trade. Live execution requires a
separately designed and reviewed contract.

## Design sources

- [Polymarket API overview](https://docs.polymarket.com/api-reference/introduction)
- [Polymarket market-data overview](https://docs.polymarket.com/market-data/overview)
- [Rewrite Your CLI for AI Agents](https://justin.poehnelt.com/posts/rewrite-your-cli-for-ai-agents/)
