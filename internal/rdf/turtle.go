package rdf

import (
	"fmt"
	"strconv"
	"strings"
)

// Prefixes is a CURIE map (prefix -> namespace).
type Prefixes map[string]string

// ParseTurtle parses a deliberately small Turtle subset:
//
//	@prefix p: <iri> .   /  PREFIX p: <iri>
//	subj pred obj (, obj)* (; pred obj)* .
//	objects: IRI, CURIE, "lit" [@lang | ^^type], number, _:b,
//	         [ pred obj ; ... ] blank nodes and (rdf lists).
//
// All quads are placed in the given named graph ("" = default graph).
func ParseTurtle(data []byte) ([]Quad, Prefixes, error) {
	return ParseTurtleGraph(data, "")
}

func ParseTurtleGraph(data []byte, graph string) ([]Quad, Prefixes, error) {
	l := newLexer(data)
	var tr []tTriple
	for {
		t, err := l.next()
		if err != nil {
			return nil, nil, err
		}
		if t == nil {
			break
		}
		if t.kind == kwPrefix || t.kind == kwBase {
			if err := l.declaration(t.kind); err != nil {
				return nil, nil, err
			}
			continue
		}
		subj, err := l.nodeTerm(t, &tr)
		if err != nil {
			return nil, nil, err
		}
		if err := l.statementTail(subj, &tr); err != nil {
			return nil, nil, err
		}
	}
	out := make([]Quad, len(tr))
	for i, x := range tr {
		out[i] = Quad{x.s, x.pr, x.o, graph}
	}
	return out, l.prefixes, nil
}

type tTriple struct{ s, pr, o Term }

const (
	tkIRI = iota
	tkLiteral
	tkNumber
	tkBlank
	tkPunct
	tkA
	kwPrefix
	kwBase
)

type token struct {
	kind  int
	val   string
	lang  string
	dtype string
}

type lexer struct {
	data     []byte
	pos      int
	prefixes Prefixes
	bn       int
	pushed   *token
	declName bool
}

func newLexer(data []byte) *lexer {
	return &lexer{data: data, prefixes: Prefixes{}}
}

func (l *lexer) declaration(kind int) error {
	name, err := l.next()
	if err != nil {
		return err
	}
	ns, err := l.next()
	if err != nil {
		return err
	}
	if kind == kwPrefix {
		if name.kind != tkPunct || !strings.HasSuffix(name.val, ":") {
			return fmt.Errorf("turtle: expected prefix name")
		}
		if ns.kind != tkIRI {
			return fmt.Errorf("turtle: prefix namespace must be IRI")
		}
		l.prefixes[strings.TrimSuffix(name.val, ":")] = ns.val
	}
	dot, err := l.next()
	if err != nil {
		return err
	}
	if dot.kind != tkPunct || dot.val != "." {
		return fmt.Errorf("turtle: expected '.' after declaration")
	}
	return nil
}

func (l *lexer) freshBlank() Term {
	l.bn++
	return NewBlank("auto" + strconv.Itoa(l.bn))
}

func (l *lexer) nodeTerm(t *token, tr *[]tTriple) (Term, error) {
	switch t.kind {
	case tkIRI:
		return NewIRI(t.val), nil
	case tkBlank:
		return NewBlank(t.val), nil
	case tkA:
		return NewIRI(RDFType), nil
	case tkNumber:
		if strings.ContainsAny(t.val, ".eE") {
			return NewTypedLiteral(t.val, XSDDecimal), nil
		}
		return NewTypedLiteral(t.val, XSDInteger), nil
	case tkLiteral:
		if t.lang != "" {
			return NewLangLiteral(t.val, t.lang), nil
		}
		if t.dtype != "" {
			return NewTypedLiteral(t.val, t.dtype), nil
		}
		return NewPlainLiteral(t.val), nil
	case tkPunct:
		switch t.val {
		case "[":
			b := l.freshBlank()
			if err := l.blankProps(b, tr); err != nil {
				return Term{}, err
			}
			return b, nil
		case "(":
			return l.collection(tr)
		}
	}
	return Term{}, fmt.Errorf("turtle: unexpected token %q", t.val)
}

func (l *lexer) blankProps(b Term, tr *[]tTriple) error {
	for {
		t, err := l.next()
		if err != nil {
			return err
		}
		if t.kind == tkPunct && t.val == "]" {
			return nil
		}
		pred, err := l.nodeTerm(t, tr)
		if err != nil {
			return err
		}
		stop, err := l.objectList(b, pred, tr)
		if err != nil {
			return err
		}
		if stop == stopClose {
			return nil
		}
	}
}

func (l *lexer) collection(tr *[]tTriple) (Term, error) {
	var items []Term
	for {
		t, err := l.next()
		if err != nil {
			return Term{}, err
		}
		if t.kind == tkPunct && t.val == ")" {
			break
		}
		item, err := l.nodeTerm(t, tr)
		if err != nil {
			return Term{}, err
		}
		items = append(items, item)
	}
	head := NewIRI(RDFNil)
	for i := len(items) - 1; i >= 0; i-- {
		cell := l.freshBlank()
		*tr = append(*tr,
			tTriple{cell, NewIRI(RDFFirst), items[i]},
			tTriple{cell, NewIRI(RDFRest), head},
		)
		head = cell
	}
	return head, nil
}

// statementTail parses  pred obj ...  after a subject; consumes closing '.'.
func (l *lexer) statementTail(subj Term, tr *[]tTriple) error {
	for {
		t, err := l.next()
		if err != nil {
			return err
		}
		if t.kind == tkPunct && t.val == "." {
			return nil
		}
		pred, err := l.nodeTerm(t, tr)
		if err != nil {
			return err
		}
		stop, err := l.objectList(subj, pred, tr)
		if err != nil {
			return err
		}
		if stop == stopDot {
			return nil
		}
	}
}

const (
	stopSemicolon = iota
	stopClose
	stopDot
)

// objectList parses objects separated by ',' and reports the terminator.
func (l *lexer) objectList(subj, pred Term, tr *[]tTriple) (int, error) {
	for {
		ot, err := l.next()
		if err != nil {
			return 0, err
		}
		obj, err := l.nodeTerm(ot, tr)
		if err != nil {
			return 0, err
		}
		*tr = append(*tr, tTriple{subj, pred, obj})
		sep, err := l.next()
		if err != nil {
			return 0, err
		}
		switch {
		case sep.kind == tkPunct && sep.val == ",":
			continue
		case sep.kind == tkPunct && sep.val == ";":
			// tolerate repeated ';'
			for {
				peek, err := l.next()
				if err != nil {
					return 0, err
				}
				if peek.kind == tkPunct && peek.val == ";" {
					continue
				}
				if peek.kind == tkPunct && peek.val == "]" {
					return stopClose, nil
				}
				if peek.kind == tkPunct && peek.val == "." {
					return stopDot, nil
				}
				l.pushed = peek
				return stopSemicolon, nil
			}
		case sep.kind == tkPunct && sep.val == "]":
			return stopClose, nil
		case sep.kind == tkPunct && sep.val == ".":
			return stopDot, nil
		default:
			return 0, fmt.Errorf("turtle: expected , ; ] . got %q", sep.val)
		}
	}
}

// ---- tokenizer ----

func (l *lexer) next() (*token, error) {
	if l.pushed != nil {
		t := l.pushed
		l.pushed = nil
		return t, nil
	}
	l.skipWS()
	if l.pos >= len(l.data) {
		return nil, nil
	}
	c := l.data[l.pos]
	switch {
	case c == '<':
		return l.iriToken()
	case c == '"' || c == '\'':
		return l.stringToken()
	case c == '+' || c == '-' || (c >= '0' && c <= '9'):
		return l.numberToken(false)
	case strings.ContainsRune("()[];,", rune(c)):
		l.pos++
		return &token{kind: tkPunct, val: string(c)}, nil
	case c == '.':
		if l.pos+1 < len(l.data) && isDigit(l.data[l.pos+1]) {
			return l.numberToken(true)
		}
		l.pos++
		return &token{kind: tkPunct, val: "."}, nil
	case c == ':':
		return l.curieToken()
	case c == '_':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == ':' {
			return l.blankToken()
		}
		return l.wordToken()
	case c == '@':
		return l.atToken()
	case isAlpha(c):
		return l.wordToken()
	}
	return nil, fmt.Errorf("turtle: unexpected char %q at %d", string(c), l.pos)
}

func (l *lexer) skipWS() {
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			l.pos++
			continue
		}
		if c == '#' {
			for l.pos < len(l.data) && l.data[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		break
	}
}

func (l *lexer) iriToken() (*token, error) {
	l.pos++ // <
	start := l.pos
	for l.pos < len(l.data) {
		if l.data[l.pos] == '>' {
			iri := string(l.data[start:l.pos])
			l.pos++
			return &token{kind: tkIRI, val: iri}, nil
		}
		if l.data[l.pos] == '\\' && l.pos+1 < len(l.data) {
			l.pos += 2
			continue
		}
		l.pos++
	}
	return nil, fmt.Errorf("turtle: unterminated IRI")
}

func (l *lexer) stringToken() (*token, error) {
	quote := l.data[l.pos]
	long := l.pos+2 < len(l.data) && l.data[l.pos+1] == quote && l.data[l.pos+2] == quote
	if long {
		l.pos += 3
	} else {
		l.pos++
	}
	var sb strings.Builder
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if long {
			if c == quote && l.pos+2 < len(l.data) && l.data[l.pos+1] == quote && l.data[l.pos+2] == quote {
				l.pos += 3
				return l.stringTail(&sb)
			}
		} else if c == quote {
			l.pos++
			return l.stringTail(&sb)
		}
		if c == '\\' && l.pos+1 < len(l.data) {
			esc := l.data[l.pos+1]
			switch esc {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\'':
				sb.WriteByte('\'')
			case '\\':
				sb.WriteByte('\\')
			case 'u', 'U':
				width := 4
				if esc == 'U' {
					width = 8
				}
				if l.pos+2+width > len(l.data) {
					return nil, fmt.Errorf("turtle: bad unicode escape")
				}
				r, err := strconv.ParseUint(string(l.data[l.pos+2:l.pos+2+width]), 16, 32)
				if err != nil {
					return nil, fmt.Errorf("turtle: bad unicode escape")
				}
				sb.WriteRune(rune(r))
				l.pos += 2 + width
				continue
			default:
				sb.WriteByte(esc)
			}
			l.pos += 2
			continue
		}
		sb.WriteByte(c)
		l.pos++
	}
	return nil, fmt.Errorf("turtle: unterminated string")
}

func (l *lexer) stringTail(sb *strings.Builder) (*token, error) {
	// no whitespace is allowed between string and ^^ / @
	if l.pos < len(l.data) && l.data[l.pos] == '^' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '^' {
		l.pos += 2
		dt, err := l.datatypeIRI()
		if err != nil {
			return nil, err
		}
		return &token{kind: tkLiteral, val: sb.String(), dtype: dt}, nil
	}
	if l.pos < len(l.data) && l.data[l.pos] == '@' {
		l.pos++
		start := l.pos
		for l.pos < len(l.data) && (isAlpha(l.data[l.pos]) || l.data[l.pos] == '-' || isDigit(l.data[l.pos])) {
			l.pos++
		}
		if start == l.pos {
			return nil, fmt.Errorf("turtle: empty language tag")
		}
		return &token{kind: tkLiteral, val: sb.String(), lang: strings.ToLower(string(l.data[start:l.pos]))}, nil
	}
	return &token{kind: tkLiteral, val: sb.String()}, nil
}

func (l *lexer) datatypeIRI() (string, error) {
	l.skipWS()
	if l.pos >= len(l.data) {
		return "", fmt.Errorf("turtle: missing datatype")
	}
	if l.data[l.pos] == '<' {
		t, err := l.iriToken()
		if err != nil {
			return "", err
		}
		return t.val, nil
	}
	start := l.pos
	for l.pos < len(l.data) && isName(l.data[l.pos]) {
		l.pos++
	}
	curie := string(l.data[start:l.pos])
	if !strings.Contains(curie, ":") {
		return "", fmt.Errorf("turtle: datatype must be IRI/CURIE")
	}
	return l.expand(curie)
}

func (l *lexer) numberToken(startedDot bool) (*token, error) {
	start := l.pos
	if !startedDot && l.pos < len(l.data) && (l.data[l.pos] == '+' || l.data[l.pos] == '-') {
		l.pos++
	}
	exp := false
	frac := startedDot
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		switch {
		case isDigit(c):
		case c == '.':
			if frac || exp {
				goto done
			}
			frac = true
		case c == 'e' || c == 'E':
			if exp {
				goto done
			}
			exp = true
			if l.pos+1 < len(l.data) && (l.data[l.pos+1] == '+' || l.data[l.pos+1] == '-') {
				l.pos++
			}
		default:
			goto done
		}
		l.pos++
	}
done:
	return &token{kind: tkNumber, val: string(l.data[start:l.pos])}, nil
}

func (l *lexer) blankToken() (*token, error) {
	l.pos += 2 // _:
	start := l.pos
	for l.pos < len(l.data) && isName(l.data[l.pos]) {
		l.pos++
	}
	if start == l.pos {
		return nil, fmt.Errorf("turtle: empty blank node label")
	}
	return &token{kind: tkBlank, val: string(l.data[start:l.pos])}, nil
}

// curieToken reads ":local" or "prefix:local".
func (l *lexer) curieToken() (*token, error) {
	start := l.pos
	for l.pos < len(l.data) && isName(l.data[l.pos]) {
		l.pos++
	}
	text := string(l.data[start:l.pos]) // includes ':'
	if l.pos < len(l.data) && l.data[l.pos] == ':' && !strings.Contains(text, ":") {
		// shouldn't happen; guard
	}
	// A trailing ':' alone (prefix declaration name like "rdfs:") is punctuation.
	if text == ":" || strings.HasSuffix(text, ":") {
		// only treat as curie when a local name follows immediately
		save := l.pos
		if l.pos < len(l.data) && isNameStart(l.data[l.pos]) {
			for l.pos < len(l.data) && isName(l.data[l.pos]) {
				l.pos++
			}
			text = string(l.data[start:l.pos])
		} else {
			_ = save
			return &token{kind: tkPunct, val: text}, nil
		}
	}
	iri, err := l.expand(strings.TrimPrefix(text, ":"))
	if err != nil {
		// bare ':' with empty prefix handled by expand using "" prefix
		return nil, err
	}
	return &token{kind: tkIRI, val: iri}, nil
}

func (l *lexer) atToken() (*token, error) {
	l.pos++ // @
	start := l.pos
	for l.pos < len(l.data) && isAlpha(l.data[l.pos]) {
		l.pos++
	}
	word := strings.ToLower(string(l.data[start:l.pos]))
	switch word {
	case "prefix":
		l.declName = true
		return &token{kind: kwPrefix}, nil
	case "base":
		return &token{kind: kwBase}, nil
	}
	return nil, fmt.Errorf("turtle: unsupported directive @%s", word)
}

func (l *lexer) wordToken() (*token, error) {
	start := l.pos
	for l.pos < len(l.data) && isName(l.data[l.pos]) {
		l.pos++
	}
	word := string(l.data[start:l.pos])
	if l.declName {
		l.declName = false
		if l.pos < len(l.data) && l.data[l.pos] == ':' {
			l.pos++
		}
		return &token{kind: tkPunct, val: word + ":"}, nil
	}
	if word == "PREFIX" || word == "BASE" {
		l.declName = word == "PREFIX"
		return &token{kind: func() int {
			if word == "PREFIX" {
				return kwPrefix
			}
			return kwBase
		}()}, nil
	}
	if l.pos < len(l.data) && l.data[l.pos] == ':' {
		// prefix:local CURIE
		l.pos++
		cstart := l.pos
		for l.pos < len(l.data) && isName(l.data[l.pos]) {
			l.pos++
		}
		local := string(l.data[cstart:l.pos])
		iri, err := l.expand(word + ":" + local)
		if err != nil {
			return nil, err
		}
		return &token{kind: tkIRI, val: iri}, nil
	}

	if word == "a" {
		return &token{kind: tkA, val: "a"}, nil
	}
	if word == "true" || word == "false" {
		return &token{kind: tkLiteral, val: word, dtype: XSDBoolean}, nil
	}
	return nil, fmt.Errorf("turtle: bare word %q (undefined prefix?)", word)
}

func (l *lexer) expand(curie string) (string, error) {
	idx := strings.Index(curie, ":")
	if idx < 0 {
		return "", fmt.Errorf("turtle: %q is not a CURIE", curie)
	}
	prefix, local := curie[:idx], curie[idx+1:]
	ns, ok := l.prefixes[prefix]
	if !ok {
		return "", fmt.Errorf("turtle: undefined prefix %q", prefix)
	}
	return ns + local, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isNameStart(c byte) bool { return isAlpha(c) || c == '_' }
func isName(c byte) bool      { return isAlpha(c) || isDigit(c) || c == '_' || c == '-' || c == '.' }
