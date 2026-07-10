package mikrotik

import "encoding/binary"

func encodeWord(word string) []byte {
	text := []byte(word)
	n := len(text)

	switch {
	case n < 0x80:
		return append([]byte{byte(n)}, text...)
	case n < 0x4000:
		b := make([]byte, 2)
		b[0] = byte((n>>8)|0x80) & 0xFF
		b[1] = byte(n) & 0xFF
		return append(b, text...)
	case n < 0x200000:
		b := make([]byte, 3)
		b[0] = byte((n>>16)|0xC0) & 0xFF
		b[1] = byte((n>>8)&0xFF) & 0xFF
		b[2] = byte(n) & 0xFF
		return append(b, text...)
	case n < 0x10000000:
		b := make([]byte, 4)
		b[0] = byte((n>>24)|0xE0) & 0xFF
		b[1] = byte((n>>16)&0xFF) & 0xFF
		b[2] = byte((n>>8)&0xFF) & 0xFF
		b[3] = byte(n) & 0xFF
		return append(b, text...)
	default:
		b := make([]byte, 5)
		b[0] = 0xF0
		binary.BigEndian.PutUint32(b[1:], uint32(n))
		return append(b, text...)
	}
}

func encodeWords(words []string) []byte {
	var result []byte
	for _, w := range words {
		result = append(result, encodeWord(w)...)
	}
	result = append(result, encodeWord("")...)
	return result
}
