package output

import (
	"fmt"
	"io"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

type NDJSONWriter struct {
	writer    io.Writer
	command   string
	maximum   int64
	written   int64
	sequence  int
	completed bool
}

func NewNDJSONWriter(writer io.Writer, command string, maximum int64) *NDJSONWriter {
	if maximum <= 0 {
		maximum = DefaultMaximumBytes
	}
	return &NDJSONWriter{writer: writer, command: command, maximum: maximum}
}

func (w *NDJSONWriter) Item(data any) error {
	if w.completed {
		return contract.Internal("cannot emit an NDJSON item after a terminal record", nil)
	}
	sequence := w.sequence
	record := contract.NDJSONRecord{
		SchemaVersion: contract.NDJSONSchemaVersion, Type: "item", Command: w.command,
		Sequence: &sequence, Data: data,
	}
	if err := w.write(record); err != nil {
		return err
	}
	w.sequence++
	return nil
}

func (w *NDJSONWriter) Summary(pagination contract.Pagination, truncation []contract.Truncation) error {
	if w.completed {
		return contract.Internal("cannot emit multiple NDJSON terminal records", nil)
	}
	record := contract.NDJSONRecord{
		SchemaVersion: contract.NDJSONSchemaVersion, Type: "summary", Command: w.command,
		Pagination: &pagination, Truncation: truncation,
	}
	if err := w.write(record); err != nil {
		return err
	}
	w.completed = true
	return nil
}

func (w *NDJSONWriter) Error(err error) error {
	if w.completed {
		return contract.Internal("cannot emit multiple NDJSON terminal records", nil)
	}
	record := contract.NDJSONRecord{
		SchemaVersion: contract.NDJSONSchemaVersion, Type: "error", Command: w.command,
		Error: contract.AsError(err), Meta: map[string]any{"itemsEmitted": w.sequence, "partial": w.sequence > 0},
	}
	if writeErr := w.write(record); writeErr != nil {
		return writeErr
	}
	w.completed = true
	return nil
}

func (w *NDJSONWriter) write(record contract.NDJSONRecord) error {
	if w.writer == nil {
		return contract.Internal("NDJSON writer is not configured", nil)
	}
	remaining := w.maximum - w.written
	if remaining <= 0 {
		return contract.NewError("output_too_large", contract.CategoryInput, "NDJSON output reached its context safety limit", contract.ExitInvalidInput)
	}
	payload, err := EncodeJSON(record, true, remaining)
	if err != nil {
		return err
	}
	written, err := w.writer.Write(payload)
	w.written += int64(written)
	if err != nil {
		return contract.Internal("could not write NDJSON output", err)
	}
	if written != len(payload) {
		return contract.Internal("could not write complete NDJSON record", io.ErrShortWrite)
	}
	return nil
}

func (w *NDJSONWriter) ItemsEmitted() int { return w.sequence }

func (w *NDJSONWriter) BytesWritten() int64 { return w.written }

func (w *NDJSONWriter) String() string {
	return fmt.Sprintf("NDJSONWriter(command=%s, items=%d, bytes=%d)", w.command, w.sequence, w.written)
}
