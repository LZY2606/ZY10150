package store

import (
	"strings"

	"gsb/internal/rdf"
)

// parseTerm decodes the N-Triples-style rendering produced by rdf.Term.
// It intentionally keeps the four term kinds apart (IRI, blank, literal
// with datatype/lang) instead of storing one flattened string column.
func parseTerm(s string) (rdf.Term, error) {
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		return rdf.IRI(unescapeIRI(s[1 : len(s)-1])), nil
	}
	if strings.HasPrefix(s, "_:") {
		return rdf.Blank(s[2:]), nil
	}
	if len(s) > 0 && s[0] == '"' {
		return parseLiteral(s)
	}
	// fallback: treat as plain literal
	return rdf.String(s), nil
}

func parseLiteral(s string) (rdf.Term, error) {
	i := 1
	var val strings.Builder
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			nc, ni := decodeEsc(s, i)
			val.WriteString(nc)
			i = ni
			continue
		}
		if c == '"' {
			i++
			break
		}
		val.WriteByte(c)
		i++
	}
	if i < len(s) && s[i] == '@' {
		return rdf.LangString(val.String(), s[i+1:]), nil
	}
	if i+1 < len(s) && s[i] == '^' && s[i+1] == '^' {
		t := s[i+2:]
		if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") {
			return rdf.Typed(val.String(), t[1:len(t)-1]), nil
		}
	}
	return rdf.String(val.String()), nil
}

func decodeEsc(s string, i int) (string, int) {
	c := s[i+1]
	switch c {
	case 'n':
		return "\n", i + 2
	case 't':
		return "\t", i + 2
	case 'r':
		return "\r", i + 2
	case '"':
		return "\"", i + 2
	case '\\':
		return "\\", i + 2
	}
	return string(c), i + 2
}

func unescapeIRI(s string) string {
	return strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(s)
}
