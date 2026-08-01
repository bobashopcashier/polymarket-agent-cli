package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

const maximumLineBytes = 8 << 10

var ErrUnavailable = errors.New("a controlling terminal is required")

type Console interface {
	Confirm(context.Context, string, string) error
	ReadSecret(context.Context, string, int) ([]byte, error)
}

type TTY struct{}

func (TTY) Confirm(ctx context.Context, summary, phrase string) error {
	ctx = nonnilContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	terminal, err := openTTY()
	if err != nil {
		return err
	}
	defer terminal.Close()
	if _, err := fmt.Fprintf(terminal, "%s\nType %q to authorize this one operation: ", summary, phrase); err != nil {
		return err
	}
	type result struct {
		line string
		err  error
	}
	completed := make(chan result)
	go func() {
		line, readErr := readBoundedLine(terminal, maximumLineBytes)
		select {
		case completed <- result{line: line, err: readErr}:
		case <-ctx.Done():
		}
	}()
	var line string
	select {
	case <-ctx.Done():
		_ = terminal.Close()
		return ctx.Err()
	case read := <-completed:
		line, err = read.line, read.err
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != phrase {
		return errors.New("authorization phrase did not match")
	}
	return nil
}

func (TTY) ReadSecret(ctx context.Context, prompt string, maximumBytes int) ([]byte, error) {
	ctx = nonnilContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maximumBytes <= 0 || maximumBytes > maximumLineBytes {
		maximumBytes = maximumLineBytes
	}
	terminal, err := openTTY()
	if err != nil {
		return nil, err
	}
	defer terminal.Close()
	if _, err := io.WriteString(terminal, prompt); err != nil {
		return nil, err
	}
	secret, err := readSecretCancelable(ctx, terminal, maximumBytes)
	_, _ = io.WriteString(terminal, "\n")
	if err != nil {
		return nil, err
	}
	if len(secret) == 0 || len(secret) > maximumBytes {
		zero(secret)
		return nil, errors.New("secret input is empty or exceeds the allowed length")
	}
	return secret, nil
}

func nonnilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func openTTY() (*os.File, error) {
	path := "/dev/tty"
	if runtime.GOOS == "windows" {
		path = "CONIN$"
	}
	terminal, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	if !term.IsTerminal(int(terminal.Fd())) {
		terminal.Close()
		return nil, ErrUnavailable
	}
	return terminal, nil
}

func readBoundedLine(reader io.Reader, maximum int) (string, error) {
	buffered := bufio.NewReader(io.LimitReader(reader, int64(maximum)+1))
	line, err := buffered.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(line) > maximum {
		return "", errors.New("terminal input exceeds the allowed length")
	}
	return line, nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
