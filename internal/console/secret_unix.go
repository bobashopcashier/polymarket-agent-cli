//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package console

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// readSecretCancelable owns terminal state and input in one goroutine. Polling
// lets a deadline restore the terminal before returning; secret bytes never
// wait in an abandoned channel.
func readSecretCancelable(ctx context.Context, terminal *os.File, maximumBytes int) (secret []byte, resultErr error) {
	state, err := term.MakeRaw(int(terminal.Fd()))
	if err != nil {
		return nil, errors.New("could not disable terminal echo")
	}
	defer func() {
		if err := term.Restore(int(terminal.Fd()), state); err != nil {
			zero(secret)
			secret = nil
			resultErr = errors.New("could not restore terminal state")
		}
	}()

	secret = make([]byte, 0, maximumBytes)
	pollDescriptors := []unix.PollFd{{Fd: int32(terminal.Fd()), Events: unix.POLLIN}}
	one := []byte{0}
	for {
		if err := ctx.Err(); err != nil {
			zero(secret)
			return nil, err
		}
		ready, err := unix.Poll(pollDescriptors, 100)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			zero(secret)
			return nil, errors.New("could not read secret input")
		}
		if ready == 0 {
			continue
		}
		count, err := terminal.Read(one)
		if err != nil || count != 1 {
			zero(secret)
			return nil, errors.New("could not read secret input")
		}
		switch one[0] {
		case '\r', '\n':
			return secret, nil
		case 3, 4: // Ctrl-C or Ctrl-D while the terminal is in raw mode.
			zero(secret)
			return nil, context.Canceled
		case 8, 127:
			if len(secret) != 0 {
				secret[len(secret)-1] = 0
				secret = secret[:len(secret)-1]
			}
		default:
			if one[0] < 0x20 {
				continue
			}
			if len(secret) >= maximumBytes {
				zero(secret)
				return nil, errors.New("secret input exceeds the allowed length")
			}
			secret = append(secret, one[0])
		}
	}
}
