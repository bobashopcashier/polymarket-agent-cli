package sanitize

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text makes untrusted provider text inert in terminals and logs. Unsafe runes
// are rendered as visible ASCII escapes instead of being interpreted.
func Text(value string) string {
	var output strings.Builder
	for len(value) > 0 {
		current, size := utf8.DecodeRuneInString(value)
		if current == utf8.RuneError && size == 1 {
			output.WriteString(`\uFFFD`)
			value = value[1:]
			continue
		}
		value = value[size:]
		if IsUnsafeRune(current) {
			if current <= 0xffff {
				fmt.Fprintf(&output, `\u%04X`, current)
			} else {
				fmt.Fprintf(&output, `\U%08X`, current)
			}
			continue
		}
		output.WriteRune(current)
	}
	return output.String()
}

func IsUnsafeRune(current rune) bool {
	return current < 0x20 || current == 0x7f || current >= 0x80 && current <= 0x9f ||
		unicode.Is(unicode.Cf, current) || current == 0x2028 || current == 0x2029
}
