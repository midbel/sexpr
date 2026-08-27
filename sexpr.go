package sexpr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

var ErrSyntax = errors.New("invalid syntax")

type Ident string

type Resolver interface {
	Resolve(string) (any, error)
}

type noopResolver struct {}

func (noopResolver) Resolve(_ string) (any, error) {
	return nil, nil
}

type Handler interface {
	BeginList() error
	EndList() error
	Atom(any) error
}

type DirectiveHandler interface {
	Handler
	Directive([]any) error
}

func Process(r io.Reader, h Handler, v Resolver) error {
	if v == nil {
		v = noopResolver{}
	}
	p, err := createParser(r, h, v)
	if err != nil {
		return err
	}
	return p.parse()
}

func Decode(r io.Reader) ([]any, error) {
	var (
		bh baseHandler
		nr noopResolver
	)
	if err := Process(r, &bh, nr); err != nil {
		return nil, err
	}
	return bh.Result(), nil
}

type baseHandler struct {
	expr   [][]any
	result []any
}

func (h *baseHandler) Clear() {
	h.expr = h.expr[:0]
	h.result = h.result[:0]
}

func (h *baseHandler) Result() []any {
	return h.result
}

func (h *baseHandler) BeginList() error {
	h.expr = append(h.expr, nil)
	return nil
}

func (h *baseHandler) EndList() error {
	if len(h.expr) == 0 {
		return fmt.Errorf("unexpected end of list")
	}
	i := len(h.expr) - 1
	list := h.expr[i]
	h.expr = h.expr[:i]

	if len(h.expr) == 0 {
		h.result = append(h.result, list)
		return nil
	}
	parent := len(h.expr) - 1
	h.expr[parent] = append(h.expr[parent], list)
	return nil
}

func (h *baseHandler) Atom(expr any) error {
	if len(h.expr) == 0 {
		h.result = append(h.result, expr)
		return nil
	}
	i := len(h.expr) - 1
	h.expr[i] = append(h.expr[i], expr)
	return nil
}

type position struct {
	Line   int
	Column int
}

type token struct {
	Literal string
	Type    rune
	position
}

type parser struct {
	handle Handler
	resolver Resolver

	scan *scanner
	curr token
	peek token
}

func createParser(r io.Reader, h Handler, v Resolver) (*parser, error) {
	scan, err := createScanner(r)
	if err != nil {
		return nil, err
	}
	p := &parser{
		scan:   scan,
		resolver: v,
		handle: h,
	}
	p.next()
	p.next()
	return p, nil
}

func (p *parser) parse() error {
	if err := p.parseDirective(); err != nil {
		return err
	}
	for {
		p.skipComments()
		if p.done() {
			break
		}
		err := p.parseExpr()
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) parseDirective() error {
	dh, ok := p.handle.(DirectiveHandler)
	if !ok {
		return nil
	}
	for !p.done() {
		p.skipComments()
		if !p.is(tokDirective) {
			break
		}
		p.next()
		expr, err := p.captureExpr()
		if err != nil {
			return err
		}
		if len(expr) != 1 {
			return ErrSyntax
		}
		arr, ok := expr[0].([]any)
		if !ok {
			arr = []any{expr[0]}
		}
		if err := dh.Directive(arr); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) captureExpr() ([]any, error) {
	var (
		old  = p.handle
		next baseHandler
	)
	defer func() {
		p.handle = old
	}()
	p.handle = &next
	if err := p.parseExpr(); err != nil {
		return nil, err
	}
	return next.Result(), nil
}

func (p *parser) parseExpr() error {
	var err error
	switch p.curr.Type {
	case tokFloat, tokInt:
		err = p.parseNumber()
	case tokString:
		err = p.parseString()
	case tokSymbol:
		err = p.parseSymbol()
	case tokBoolean:
		err = p.parseBool()
	case tokVariable:
		err = p.parseVariable()
	case tokBegList:
		err = p.parseList()
	default:
		err = fmt.Errorf("unexpected token type")
	}
	return err
}

func (p *parser) parseVariable() error {
	val, err := p.resolver.Resolve(p.curr.Literal)
	if err == nil {
		err = p.handle.Atom(val)
		p.next()
	}
	return err
}

func (p *parser) parseNumber() error {
	defer p.next()
	var (
		val any
		err error
	)
	if p.is(tokInt) {
		val, err = strconv.ParseInt(p.curr.Literal, 0, 64)
	} else {
		val, err = strconv.ParseFloat(p.curr.Literal, 64)
	}
	if err == nil {
		err = p.handle.Atom(val)
	}
	return err
}

func (p *parser) parseString() error {
	defer p.next()
	return p.handle.Atom(p.curr.Literal)
}

func (p *parser) parseSymbol() error {
	defer p.next()
	return p.handle.Atom(Ident(p.curr.Literal))
}

func (p *parser) parseBool() error {
	defer p.next()
	val, err := strconv.ParseBool(p.curr.Literal)
	if err == nil {
		err = p.handle.Atom(val)
	}
	return err
}

func (p *parser) parseList() error {
	p.next()
	if err := p.handle.BeginList(); err != nil {
		return err
	}
	for !p.done() && !p.is(tokEndList) {
		err := p.parseExpr()
		if err != nil {
			return err
		}
	}
	if !p.is(tokEndList) {
		return fmt.Errorf("expected closing parenthesis at end of list")
	}
	p.next()
	return p.handle.EndList()
}

func (p *parser) next() {
	p.curr = p.peek
	p.peek = p.scan.scan()
}

func (p *parser) done() bool {
	return p.is(tokEof)
}

func (p *parser) skipComments() {
	for p.is(tokComment) {
		p.next()
	}
}

func (p *parser) is(r rune) bool {
	return p.curr.Type == r
}

const (
	tokEof rune = -(1 + iota)
	tokSymbol
	tokString
	tokFloat
	tokInt
	tokBoolean
	tokBegList
	tokEndList
	tokComment
	tokDirective
	tokVariable
	tokInvalid
)

type scanner struct {
	input []byte
	char  rune
	curr  int
	next  int

	buf *bytes.Buffer
	position
}

func createScanner(r io.Reader) (*scanner, error) {
	input, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s := &scanner{
		input: input,
		buf:   new(bytes.Buffer),
	}
	s.position.Line++
	s.read()
	return s, nil
}

func (s *scanner) scan() token {
	var tok token
	if s.done() {
		tok.Type = tokEof
		return tok
	}
	defer s.reset()

	s.skipBlank()
	tok.position = s.position
	switch {
	case isComment(s.char):
		s.scanComment(&tok)
	case isParen(s.char):
		s.scanDelimiter(&tok)
	case isQuote(s.char):
		s.scanString(&tok)
	case s.char == underscore || isLetter(s.char):
		s.scanLiteral(&tok)
	case s.char == minus || isDigit(s.char):
		s.scanNumber(&tok)
	case isDirective(s.char, s.peek()):
		s.scanDirective(&tok)
	case isVariable(s.char, s.peek()):
		s.scanVariable(&tok)
	default:
		tok.Type = tokInvalid
	}
	return tok
}

func (s *scanner) scanVariable(tok *token) {
	s.read()
	s.read()
	for !s.done() && s.char != rcurly {
		s.write()
		s.read()
	}
	tok.Literal = s.literal()
	tok.Type = tokVariable
	if s.char != rcurly {
		tok.Type = tokInvalid
	} else {
		s.read()
	}
}

func (s *scanner) scanDirective(tok *token) {
	s.read()
	s.read()
	tok.Type = tokDirective
}

func (s *scanner) scanComment(tok *token) {
	s.read()
	s.skipSpace()
	for !s.done() && !isNL(s.char) {
		s.write()
		s.read()
	}
	tok.Type = tokComment
	tok.Literal = s.literal()
}

func (s *scanner) scanString(tok *token) {
	s.read()
	for !s.done() && !isQuote(s.char) {
		if s.char == backslash {
			switch c := s.peek(); c {
			case quote:
				s.writeRune(quote)
			case backslash:
				s.writeRune(backslash)
			case 'n':
				s.writeRune(nl)
			case 'r':
				s.writeRune(cr)
			case 't':
				s.writeRune(tab)
			default:
				tok.Type = tokInvalid
				return
			}
			s.read()
		} else {
			s.write()
		}
		s.read()
	}
	tok.Type = tokString
	tok.Literal = s.literal()

	if s.char != quote {
		tok.Type = tokInvalid
	} else {
		s.read()
	}
}

func (s *scanner) scanLiteral(tok *token) {
	for !s.done() && isAlpha(s.char) {
		s.write()
		s.read()
	}
	tok.Type = tokSymbol
	tok.Literal = s.literal()
	if tok.Literal == "true" || tok.Literal == "false" {
		tok.Type = tokBoolean
	}
}

func (s *scanner) scanNumber(tok *token) {
	if s.char == minus {
		s.write()
		s.read()
	}
	if s.char == '0' && s.peek() == s.char {
		tok.Type = tokInvalid
		return
	}
	for !s.done() && isDigit(s.char) {
		s.write()
		s.read()
	}
	tok.Type = tokInt
	tok.Literal = s.literal()
	if s.char != dot {
		return
	}
	s.write()
	s.read()
	if !isDigit(s.char) {
		tok.Type = tokInvalid
		return
	}
	for !s.done() && isDigit(s.char) {
		s.write()
		s.read()
	}
	tok.Literal = s.literal()
}

func (s *scanner) scanDelimiter(tok *token) {
	switch s.char {
	case lparen:
		tok.Type = tokBegList
	case rparen:
		tok.Type = tokEndList
	default:
		tok.Type = tokInvalid
	}
	if tok.Type == tokInvalid {
		return
	}
	s.read()
}

func (s *scanner) read() {
	if s.curr >= len(s.input) {
		s.char = utf8.RuneError
		return
	}
	c, n := utf8.DecodeRune(s.input[s.next:])
	if c == utf8.RuneError {
		s.char = c
		s.next = len(s.input)
	}
	s.char, s.curr, s.next = c, s.next, s.next+n

	if s.char == nl {
		s.Line += 1
		s.Column = 0
	}
	s.Column++
}

func (s *scanner) peek() rune {
	c, _ := utf8.DecodeRune(s.input[s.next:])
	return c
}

func (s *scanner) done() bool {
	return s.char == utf8.RuneError
}

func (s *scanner) writeRune(r rune) {
	s.buf.WriteRune(r)
}

func (s *scanner) write() {
	s.writeRune(s.char)
}

func (s *scanner) reset() {
	s.buf.Reset()
}

func (s *scanner) literal() string {
	return s.buf.String()
}

func (s *scanner) skipSpace() {
	for isSpace(s.char) {
		s.read()
	}
}

func (s *scanner) skipBlank() {
	for isBlank(s.char) {
		s.read()
	}
}

const (
	underscore = '_'
	minus      = '-'
	semicolon  = ';'
	space      = ' '
	tab        = '\t'
	nl         = '\n'
	cr         = '\r'
	lparen     = '('
	rparen     = ')'
	dot        = '.'
	quote      = '"'
	bang       = '!'
	pound      = '#'
	backslash  = '\\'
	dollar     = '$'
	lcurly     = '{'
	rcurly     = '}'
)

func isVariable(r, k rune) bool {
	return r == dollar && r == lcurly
}

func isDirective(r, k rune) bool {
	return r == pound && k == bang
}

func isQuote(r rune) bool {
	return r == quote
}

func isParen(r rune) bool {
	return r == lparen || r == rparen
}

func isComment(r rune) bool {
	return r == semicolon
}

func isLetter(r rune) bool {
	return isLower(r) || isUpper(r)
}

func isLower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isAlpha(r rune) bool {
	return isLetter(r) || isDigit(r) || r == underscore
}

func isBlank(r rune) bool {
	return isSpace(r) || isNL(r)
}

func isSpace(r rune) bool {
	return r == space || r == tab
}

func isNL(r rune) bool {
	return r == nl || r == cr
}
