package mikrotik

func decodeWord(buf []byte) []string {
	var words []string
	offset := 0

	for offset < len(buf) {
		lengthByte := buf[offset]
		var wordLength int
		headerSize := 1

		switch {
		case lengthByte&0x80 == 0x00:
			wordLength = int(lengthByte)
		case lengthByte&0xC0 == 0x80:
			if offset+1 >= len(buf) {
				return words
			}
			wordLength = (int(lengthByte&0x3F) << 8) + int(buf[offset+1])
			headerSize = 2
		case lengthByte&0xE0 == 0xC0:
			if offset+2 >= len(buf) {
				return words
			}
			wordLength = (int(lengthByte&0x1F) << 16) + (int(buf[offset+1]) << 8) + int(buf[offset+2])
			headerSize = 3
		case lengthByte&0xF0 == 0xE0:
			if offset+3 >= len(buf) {
				return words
			}
			wordLength = (int(lengthByte&0x0F) << 24) + (int(buf[offset+1]) << 16) + (int(buf[offset+2]) << 8) + int(buf[offset+3])
			headerSize = 4
		case lengthByte&0xF8 == 0xF0:
			if offset+4 >= len(buf) {
				return words
			}
			wordLength = (int(buf[offset+1]) << 24) + (int(buf[offset+2]) << 16) + (int(buf[offset+3]) << 8) + int(buf[offset+4])
			headerSize = 5
		default:
			return words
		}

		if wordLength == 0 {
			words = append(words, "")
			offset += headerSize
			continue
		}

		start := offset + headerSize
		end := start + wordLength
		if end > len(buf) {
			break
		}

		words = append(words, string(buf[start:end]))
		offset = end
	}

	return words
}
