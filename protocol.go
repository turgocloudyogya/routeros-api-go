package mikrotik

import "strings"

type QueryResult map[string]string

func buildCommand(words []string) []byte {
	return encodeWords(words)
}

func parseResponse(words []string) []QueryResult {
	if len(words) == 0 {
		return nil
	}

	if words[0] == "!done" {
		return []QueryResult{{"success": "true"}}
	}

	trapIdx := -1
	for i, w := range words {
		if w == "!trap" {
			trapIdx = i
			break
		}
	}
	if trapIdx != -1 {
		attrs := make(QueryResult)
		for _, w := range words[trapIdx+1:] {
			if strings.HasPrefix(w, "=.") {
				w = w[2:]
			} else if strings.HasPrefix(w, "=") {
				w = w[1:]
			}
			eqIdx := strings.Index(w, "=")
			if eqIdx > 0 {
				attrs[w[:eqIdx]] = w[eqIdx+1:]
			} else if eqIdx == -1 {
				attrs[w] = ""
			}
		}
		return []QueryResult{attrs}
	}

	var reIndices []int
	for i, w := range words {
		if w == "!re" {
			reIndices = append(reIndices, i)
		}
	}

	if len(reIndices) == 0 {
		hasData := false
		for _, w := range words {
			if !strings.HasPrefix(w, "!") && w != "" {
				hasData = true
				break
			}
		}
		if hasData {
			reIndices = append(reIndices, -1)
		}
	}

	var results []QueryResult

	for i := 0; i < len(reIndices); i++ {
		start := reIndices[i] + 1
		end := len(words)
		if i+1 < len(reIndices) {
			end = reIndices[i+1]
		}

		if start >= end {
			continue
		}

		entry := make([]string, 0)
		for _, w := range words[start:end] {
			if !strings.HasPrefix(w, "!") && w != "" {
				entry = append(entry, w)
			}
		}

		obj := make(QueryResult)
		for _, prop := range entry {
			cleaned := prop
			if strings.HasPrefix(cleaned, "=.") {
				cleaned = cleaned[2:]
			} else if strings.HasPrefix(cleaned, "=") {
				cleaned = cleaned[1:]
			}
			eqIdx := strings.Index(cleaned, "=")
			if eqIdx > 0 {
				key := cleaned[:eqIdx]
				value := cleaned[eqIdx+1:]
				obj[key] = value
			} else if eqIdx == -1 {
				obj[cleaned] = ""
			}
		}
		results = append(results, obj)
	}

	return results
}
