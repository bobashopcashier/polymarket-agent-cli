package output

import (
	"fmt"
	"io"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/sanitize"
)

type Format string

const (
	FormatHuman  Format = "human"
	FormatJSON   Format = "json"
	FormatNDJSON Format = "ndjson"
)

type Options struct {
	Format       Format
	Compact      bool
	Fields       string
	MaximumBytes int64
}

type Writer struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Options Options
}

func NewWriter(stdout, stderr io.Writer, options Options) *Writer {
	if options.MaximumBytes <= 0 {
		options.MaximumBytes = DefaultMaximumBytes
	}
	return &Writer{Stdout: stdout, Stderr: stderr, Options: options}
}

func (w *Writer) Success(envelope contract.SuccessEnvelope, allowedFields []string) error {
	if w == nil || w.Stdout == nil {
		return contract.Internal("stdout is not configured", nil)
	}
	if w.Options.Fields != "" {
		projected, err := ProjectEnvelope(envelope, w.Options.Fields, allowedFields)
		if err != nil {
			return err
		}
		envelope = projected
	}
	switch w.Options.Format {
	case FormatHuman:
		return writeHuman(w.Stdout, envelope, w.Options.MaximumBytes)
	case FormatJSON, "":
		return WriteJSON(w.Stdout, envelope, w.Options.Compact, w.Options.MaximumBytes)
	case FormatNDJSON:
		return contract.Invalid("invalid_output_format", "collection handlers must use NDJSONWriter for --ndjson")
	default:
		return contract.Invalid("invalid_output_format", fmt.Sprintf("unknown output format %q", w.Options.Format))
	}
}

func (w *Writer) Failure(command string, err error) (contract.ExitCode, error) {
	appErr := contract.AsError(err)
	if w == nil || w.Stderr == nil {
		return appErr.ExitCode, contract.Internal("stderr is not configured", nil)
	}
	if w.Options.Format == FormatHuman {
		if _, writeErr := fmt.Fprintf(w.Stderr, "error: %s\n", sanitize.Text(appErr.Message)); writeErr != nil {
			return appErr.ExitCode, contract.Internal("could not write error output", writeErr)
		}
		if appErr.Hint != "" {
			if _, writeErr := fmt.Fprintf(w.Stderr, "hint: %s\n", sanitize.Text(appErr.Hint)); writeErr != nil {
				return appErr.ExitCode, contract.Internal("could not write error hint", writeErr)
			}
		}
		return appErr.ExitCode, nil
	}
	writeErr := WriteJSON(w.Stderr, contract.Failure(command, appErr), w.Options.Compact, w.Options.MaximumBytes)
	return appErr.ExitCode, writeErr
}

func writeHuman(writer io.Writer, envelope contract.SuccessEnvelope, maximum int64) error {
	safe, err := sanitize.Value(envelope.Data)
	if err != nil {
		return err
	}
	return WriteJSON(writer, safe, false, maximum)
}
