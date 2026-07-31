#!/bin/sh
set -eu

# Official polymarket-cli revision that merged production CLOB V2 support.
revision='9b18b5faf5493b945c48ca22efaf9645f0c69ab8'

exec cargo install \
  --git https://github.com/Polymarket/polymarket-cli \
  --rev "$revision" \
  --locked \
  polymarket-cli
