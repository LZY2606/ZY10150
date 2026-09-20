package rdf

import (
	"errors"
	"strconv"
)

var (
	errUnterminated = errors.New("unterminated string literal")
	errNewline      = errors.New("newline in single-line string literal")
)

func readEscape(s string, i int) (string, int) {
	if i+1 >= len(s) {
		return "\\", i + 1
	}
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
	case '\'':
		return "'", i + 2
	case '\\':
		return "\\", i + 2
	case 'u':
		if i+5 < len(s) {
			if v, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
				return string(rune(v)), i + 6
			}
		}
		return "", i + 6
	case 'U':
		if i+9 < len(s) {
			if v, err := strconv.ParseUint(s[i+2:i+10], 16, 64); err == nil {
				return string(rune(v)), i + 10
			}
		}
		return "", i + 10
	}
	return string(c), i + 2
}

func (p *ttParser) errAt(off int, format string, args ...any) error {
	head := p.src[:off]
	line := 1 + countByte(head, '\n')
	col := off - lastIndex(head, '\n')
	return formatErr(line, col, format, args...)
}

func countByte(s string, b byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			n++
		}
	}
	return n
}
func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
