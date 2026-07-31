# Development

## Verification

Run the same checks as CI:

```bash
make check
```

The check target verifies formatting without rewriting files, runs `go vet`,
executes the normal and race-enabled test suites, and builds `dist/pmx`.

Individual targets are also available:

```bash
make fmt-check
make vet
make test
make race
make build
```

## Contract changes

The command registry is the source of truth. When adding or changing a command:

1. Define the request and response contracts in the registry.
2. Publish input limits, output limits, effects, and stable errors.
3. Keep schema, parser, and provider mappings derived from or tested against the
   same definition.
4. Add fake-transport or fake-executable tests. Unit and CI tests must not
   contact live providers, the real keychain, or a production wallet.
5. Add hostile-input, output-boundary, sanitization, and JSON stream-purity
   coverage.
6. Update README examples and the bundled skill only after the contract is
   executable and tested.

## Safety rules

- Never accept private keys or seed phrases in argv or raw JSON.
- Never add a network mutation without a proven no-network, no-keychain dry-run
  path and controlling-terminal authorization for execution.
- Never accept private keys through argv, `--params`, ordinary stdin, JSON
  output, logs, or error causes.
- Treat a configured execution-engine path as privileged because the child
  receives a private key for authenticated operations.
- Classify uncertain mutation failures as indeterminate and require
  reconciliation instead of retry.
- Never map a provider failure or partial result to exit `0`.
- Never write logs, progress, or errors into JSON stdout.
- Never allow a user identifier to supply a host, path traversal, query, or
  fragment.
- Keep response and output limits testable at the exact boundary.

## Dependencies

Prefer the Go standard library. Current security dependencies are go-ethereum
for secp256k1 and Ethereum transaction handling, go-keyring for native keychain
storage, and x/term for masked terminal input. New dependencies need a narrow
purpose, active maintenance, and a clear safety benefit.
