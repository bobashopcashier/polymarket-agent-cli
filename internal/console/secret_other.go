//go:build windows || plan9 || js || wasip1

package console

import (
	"context"
	"errors"
	"os"
)

func readSecretCancelable(ctx context.Context, terminal *os.File, _ int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = terminal
	return nil, errors.New("masked secret input with a deadline is unsupported on this platform")
}
