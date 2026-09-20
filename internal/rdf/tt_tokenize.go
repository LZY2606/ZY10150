package rdf

import (
	"strings"
	"unicode"
)

type tokKind int

const (
	tkIRI tokKind = iota
	tkPName
	tkBlank
	tkString
	tkNumber
	tkBool
	tkDot
	tkSemi
	tkComma
	tkLB
	tkRB
	tkLP
	tkRP
	tkLC
	tkRC
	tkCaret
	tkAt
	tkWord // bare word such as "a", "PREFIX", "GRAPH"
)

type tok struct {
	kind tokKind
	val  string
	off  int
}

func (p *ttParser) tokenize() error {
	s := p.src
	i := 0
	for i < len(s) {
		r := rune(s[i])
		if r == '#' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if unicode.IsSpace(r) || r == '\uFEFF' {
			i++
			continue
		}
		start := i
		switch {
		case s[i] == '.':
			p.toks = append(p.toks, tok{tkDot, ".", start})
			i++
		case s[i] == ';':
			p.toks = append(p.toks, tok{tkSemi, ";", start})
			i++
		case s[i] == ',':
			p.toks = append(p.toks, tok{tkComma, ",", start})
			i++
		case s[i] == '[':
			p.toks = append(p.toks, tok{tkLB, "[", start})
			i++
		case s[i] == ']':
			p.toks = append(p.toks, tok{tkRB, "]", start})
			i++
		case s[i] == '(':
			p.toks = append(p.toks, tok{tkLP, "(", start})
			i++
		case s[i] == ')':
			p.toks = append(p.toks, tok{tkRP, ")", start})
			i++
		case s[i] == '{':
			p.toks = append(p.toks, tok{tkLC, "{", start})
			i++
		case s[i] == '}':
			p.toks = append(p.toks, tok{tkRC, "}", start})
			i++
		case s[i] == '^':
			p.toks = append(p.toks, tok{tkCaret, "^", start})
			i++
		case s[i] == '@':
			j := i + 1
			for j < len(s) && isNameByte(s[j]) {
				j++
			}
			p.toks = append(p.toks, tok{tkAt, strings.ToLower(s[i+1 : j]), start})
			i = j
		case s[i] == '<':
			j := i + 1
			for j < len(s) && s[j] != '>' {
				if s[j] == '\\' {
					j += 2
					continue
				}
				j++
			}
			if j >= len(s) {
				return p.errAt(start, "unterminated IRI")
			}
			p.toks = append(p.toks, tok{tkIRI, s[i+1 : j], start})
			i = j + 1
		case s[i] == '"' || (s[i] == '\''):
			quote := s[i]
			long := i+2 < len(s) && s[i+1] == byte(quote) && s[i+2] == byte(quote)
			var val string
			var ni int
			var err error
			if long {
				val, ni, err = p.readLongString(s, i+3, byte(quote))
			} else {
				val, ni, err = p.readString(s, i+1, byte(quote))
			}
			if err != nil {
				return p.errAt(start, err.Error())
			}
			p.toks = append(p.toks, tok{tkString, val, start})
			i = ni
		case s[i] == '_' && i+1 < len(s) && s[i+1] == ':':
			j := i + 2
			for j < len(s) && isNameByte(s[j]) {
				j++
			}
			p.toks = append(p.toks, tok{tkBlank, s[i+2 : j], start})
			i = j
		case s[i] == '+' || s[i] == '-' || (s[i] >= '0' && s[i] <= '9'):
			j := i + 1
			dot := false
			for j < len(s) {
				c := s[j]
				if c >= '0' && c <= '9' {
					j++
				} else if c == '.' && !dot && j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
					dot = true
					j++
				} else if c == 'e' || c == 'E' {
					j++
					if j < len(s) && (s[j] == '+' || s[j] == '-') {
						j++
					}
				} else {
					break
				}
			}
			p.toks = append(p.toks, tok{tkNumber, s[i:j], start})
			i = j
		default:
			j := i
			for j < len(s) && isPNameByte(s[j]) {
				j++
			}
			if j == i {
				return p.errAt(start, "unexpected character %q", string(r))
			}
			p.toks = append(p.toks, tok{tkWord, s[i:j], start})
			i = j
		}
	}
	return nil
}

func (p *ttParser) readString(s string, i int, q byte) (string, int, error) {
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		if c == q {
			return b.String(), i + 1, nil
		}
		if c == '\\' && i+1 < len(s) {
			nc, ni := readEscape(s, i)
			b.WriteString(nc)
			i = ni
			continue
		}
		if c == '\n' {
			return "", i, errNewline
		}
		b.WriteByte(c)
		i++
	}
	return "", i, errUnterminated
}

func (p *ttParser) readLongString(s string, i int, q byte) (string, int, error) {
	var b strings.Builder
	for i < len(s) {
		if i+2 < len(s) && s[i] == q && s[i+1] == q && s[i+2] == q {
			return b.String(), i + 3, nil
		}
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			nc, ni := readEscape(s, i)
			b.WriteString(nc)
			i = ni
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", i, errUnterminated
}

func isNameByte(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isPNameByte(c byte) bool {
	if isNameByte(c) {
		return true
	}
	switch c {
	case ':', '.', '%', '/', '#', '?', '~', '&', '=', '+', '$', '!', '*', '\'', '@':
		return true
	}
	return false
}

var _ = unicode.IsLetter
