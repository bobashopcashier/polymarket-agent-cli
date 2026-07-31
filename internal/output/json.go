package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

const DefaultMaximumBytes int64 = 8 << 20

// EncodeJSON buffers before returning so an oversize document is never partly
// emitted. The returned payload always ends with one newline.
func EncodeJSON(value any, compact bool, maximum int64) ([]byte, error) {
	if maximum <= 0 {
		maximum = DefaultMaximumBytes
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, contract.Internal("could not encode JSON output", err)
	}
	if int64(buffer.Len()) > maximum {
		return nil, contract.NewError(
			"output_too_large", contract.CategoryInput,
			fmt.Sprintf("JSON output exceeds the %d-byte context safety limit", maximum),
			contract.ExitInvalidInput,
		).WithHint("Retry with --fields, --compact, or smaller pagination bounds.")
	}
	return buffer.Bytes(), nil
}

func WriteJSON(writer io.Writer, value any, compact bool, maximum int64) error {
	payload, err := EncodeJSON(value, compact, maximum)
	if err != nil {
		return err
	}
	_, err = writer.Write(payload)
	if err != nil {
		return contract.Internal("could not write JSON output", err)
	}
	return nil
}
