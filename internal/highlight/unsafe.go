package highlight

import "unsafe"

// bytesToString reinterprets b as a string without copying. The lexer only
// reads, and callers never mutate the buffer after handing it over, so this is
// safe and saves a full copy of every previewed file.
func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
