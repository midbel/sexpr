package sexpr

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"strconv"
	"time"
	"unicode"
)

var (
	ErrSyntax = errors.New("invalid syntax")
	ErrInput  = errors.New("bad input")
)

type Ident string

type Resolver interface {
	Resolve(string) (any, error)
}

type noopResolver struct{}

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

type parser struct {
	handle   Handler
	resolver Resolver

	scan *scanner
	curr Token
	peek Token
}

func createParser(r io.Reader, h Handler, v Resolver) (*parser, error) {
	scan, err := createScanner(r)
	if err != nil {
		return nil, err
	}
	p := &parser{
		scan:     scan,
		resolver: v,
		handle:   h,
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
		if !p.is(TokDirective) {
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
	case TokDate:
		err = p.parseDate()
	case TokDateTime:
		err = p.parseDateTime()
	case TokFloat, TokInt:
		err = p.parseNumber()
	case TokString:
		err = p.parseString()
	case TokSymbol:
		err = p.parseSymbol()
	case TokBoolean:
		err = p.parseBool()
	case TokVariable:
		err = p.parseVariable()
	case TokBegList:
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

func (p *parser) parseDate() error {
	defer p.next()
	val, err := time.Parse(time.DateOnly, p.curr.Literal)
	if err == nil {
		p.handle.Atom(val)
	}
	return err
}

func (p *parser) parseDateTime() error {
	defer p.next()
	val, err := time.Parse(time.RFC3339, p.curr.Literal)
	if err == nil {
		p.handle.Atom(val)
	}
	return err
}

func (p *parser) parseNumber() error {
	defer p.next()
	var (
		val any
		err error
	)
	if p.is(TokInt) {
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
	for !p.done() && !p.is(TokEndList) {
		err := p.parseExpr()
		if err != nil {
			return err
		}
	}
	if !p.is(TokEndList) {
		return fmt.Errorf("expected closing parenthesis at end of list")
	}
	p.next()
	return p.handle.EndList()
}

func (p *parser) next() {
	p.curr = p.peek
	p.peek = p.scan.Scan()
}

func (p *parser) done() bool {
	return p.is(TokEof)
}

func (p *parser) skipComments() {
	for p.is(TokComment) {
		p.next()
	}
}

func (p *parser) is(r Type) bool {
	return p.curr.Type == r
}

type Position struct {
	Line   int
	Column int
}

func (p Position) String() string {
	return fmt.Sprintf("%02d:%02d", p.Line, p.Column)
}

type TokenStats struct {
	Idents     int
	Strings    int
	Ints       int
	Floats     int
	Dates      int
	DateTimes  int
	Bools      int
	Lists      int
	Vars       int
	Directives int
	Comments   int
	Invalid    bool
}

func Stats(r io.Reader) TokenStats {
	var (
		scan, _ = createScanner(r)
		stat    TokenStats
	)
	for !stat.Invalid {
		tok := scan.Scan()
		if tok.Type == TokEof {
			break
		}
		switch tok.Type {
		case TokInvalid:
			stat.Invalid = true
		case TokSymbol:
			stat.Idents++
		case TokString:
			stat.Strings++
		case TokFloat:
			stat.Floats++
		case TokInt:
			stat.Ints++
		case TokBoolean:
			stat.Bools++
		case TokBegList:
			stat.Lists++
		case TokComment:
			stat.Comments++
		case TokDirective:
			stat.Directives++
		case TokVariable:
			stat.Vars++
		}
	}
	return stat
}

type Token struct {
	Literal string
	Type    Type
	Position
}

type Type rune

const (
	TokEof Type = -(1 + iota)
	TokSymbol
	TokString
	TokFloat
	TokInt
	TokBoolean
	TokBegList
	TokEndList
	TokComment
	TokDirective
	TokVariable
	TokDate
	TokDateTime
	TokInvalid
)

func (t Type) String() string {
	var str string
	switch t {
	case TokEof:
		str = "eof"
	case TokSymbol:
		str = "identifier"
	case TokString:
		str = "string"
	case TokFloat:
		str = "float"
	case TokInt:
		str = "integer"
	case TokBoolean:
		str = "boolean"
	case TokDate:
		str = "date"
	case TokDateTime:
		str = "datetime"
	case TokBegList:
		str = "("
	case TokEndList:
		str = ")"
	case TokComment:
		str = "comment"
	case TokDirective:
		str = "#!"
	case TokVariable:
		str = "variable"
	case TokInvalid:
		str = "invalid"
	default:
		str = "?"
	}
	return str
}

type scanner struct {
	input *bufio.Reader
	err   error
	char  rune

	buf *bytes.Buffer
	Position
}

func Lex(r io.Reader) (iter.Seq2[Token, error], error) {
	scan, err := createScanner(r)
	if err != nil {
		return nil, err
	}
	it := func(yield func(Token, error) bool) {
		for {
			tok := scan.Scan()
			if tok.Type == TokEof {
				break
			}
			if errors.Is(scan.Err(), io.EOF) {
				break
			}
			if !yield(tok, scan.Err()) {
				break
			}
		}
	}
	return it, nil
}

func createScanner(r io.Reader) (*scanner, error) {
	input := bufio.NewReader(r)
	s := &scanner{
		input: input,
		buf:   new(bytes.Buffer),
	}
	s.Position.Line++
	s.advance()
	return s, nil
}

func (s *scanner) Err() error {
	return s.err
}

func (s *scanner) Scan() Token {
	var tok Token
	if s.err != nil && !s.done() {
		tok.Type = TokInvalid
		return tok
	}
	s.skipBlank()
	if s.done() {
		tok.Type = TokEof
		return tok
	}
	defer s.reset()

	tok.Position = s.Position
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
		tok.Type = TokInvalid
	}
	if tok.Type == TokInvalid {
		s.err = ErrInput
		if tok.Literal == "" {
			tok.Literal = s.literal()
		}
	}
	return tok
}

func (s *scanner) scanVariable(tok *Token) {
	s.advance()
	s.advance()
	for !s.done() && s.char != rcurly {
		s.write()
		s.advance()
	}
	tok.Literal = s.literal()
	tok.Type = TokVariable
	if s.char != rcurly {
		tok.Type = TokInvalid
	} else {
		s.advance()
	}
}

func (s *scanner) scanDirective(tok *Token) {
	s.advance()
	s.advance()
	tok.Type = TokDirective
}

func (s *scanner) scanComment(tok *Token) {
	s.advance()
	s.skipSpace()
	for !s.done() && !isNL(s.char) {
		s.write()
		s.advance()
	}
	tok.Type = TokComment
	tok.Literal = s.literal()
}

func (s *scanner) scanString(tok *Token) {
	s.advance()
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
				s.err = ErrInput
				tok.Type = TokInvalid
				return
			}
			s.advance()
		} else {
			s.write()
		}
		s.advance()
	}
	tok.Type = TokString
	tok.Literal = s.literal()

	if s.char != quote {
		tok.Type = TokInvalid
	} else {
		s.advance()
	}
}

func (s *scanner) scanLiteral(tok *Token) {
	for !s.done() && (isAlpha(s.char) || s.char == minus) {
		s.write()
		s.advance()
	}
	tok.Type = TokSymbol
	tok.Literal = s.literal()
	if tok.Literal == "true" || tok.Literal == "false" {
		tok.Type = TokBoolean
	}
}

func (s *scanner) scanNumber(tok *Token) {
	if isSign(s.char) {
		s.scanDecimal(tok)
		return
	}
	if s.char == '0' {
		switch s.peek() {
		case 'x', 'X':
			s.scanHexa(tok)
		case 'o', 'O':
			s.scanOctal(tok)
		default:
			s.scanDecimal(tok)
		}
	} else {
		s.scanDecimal(tok)
	}
}

func (s *scanner) scanHexa(tok *Token) {
	s.write()
	s.advance()
	s.write()
	s.advance()

	if !isHexa(s.char) {
		tok.Type = TokInvalid
		return
	}
	reco := newBaseNumberRecognizer(isHexa)
	for !s.done() && (isHexa(s.char) || s.char == underscore) {
		reco.transition(s.char)
		if s.char != underscore {
			s.write()
		}
		s.advance()
	}
	tok.Type = reco.typeOf()
	tok.Literal = s.literal()
}

func (s *scanner) scanOctal(tok *Token) {
	s.write()
	s.advance()
	s.write()
	s.advance()
	if !isOctal(s.char) {
		tok.Type = TokInvalid
		return
	}
	reco := newBaseNumberRecognizer(isHexa)
	for !s.done() && (isOctal(s.char) || s.char == underscore) {
		reco.transition(s.char)
		if s.char != underscore {
			s.write()
		}
		s.advance()
	}
	tok.Type = TokInt
	tok.Literal = s.literal()
	if !reco.valid() {
		tok.Type = TokInvalid
	}
}

func (s *scanner) scanDecimal(tok *Token) {
	if isSign(s.char) {
		if s.char == minus {
			s.write()
		}
		s.advance()
	}
	if s.char == '0' && s.peek() == s.char {
		tok.Type = TokInvalid
		return
	}
	until := func(char rune) bool {
		return isBlank(char) || isComment(char) || isDelim(char)
	}
	reco := newDecimalNumberRecognizer(decimalStateNumber)
	for !s.done() && !until(s.char) {
		reco.transition(s.char)
		if !reco.valid() {
			break
		}
		if s.char != underscore {
			s.write()
		}
		s.advance()
	}
	tok.Type = reco.typeOf()
	tok.Literal = s.literal()
}

func (s *scanner) scanDelimiter(tok *Token) {
	switch s.char {
	case lparen:
		tok.Type = TokBegList
	case rparen:
		tok.Type = TokEndList
	default:
		tok.Type = TokInvalid
	}
	if tok.Type == TokInvalid {
		return
	}
	s.advance()
}

func (s *scanner) advance() {
	if s.err != nil {
		return
	}
	c, _, err := s.input.ReadRune()
	if err != nil {
		s.err = err
		return
	}
	s.char = c
	if s.char == cr && s.peek() == nl {
		s.char, _, _ = s.input.ReadRune()
	}

	if isNL(s.char) {
		s.Line += 1
		s.Column = 0
	}
	s.Column++
}

func (s *scanner) peek() rune {
	c, _, err := s.input.ReadRune()
	if err != nil {
		return 0
	}
	s.input.UnreadRune()
	return c
}

func (s *scanner) done() bool {
	return errors.Is(s.err, io.EOF)
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
		s.advance()
	}
}

func (s *scanner) skipBlank() {
	for isBlank(s.char) {
		s.advance()
	}
}

const (
	underscore = '_'
	minus      = '-'
	plus       = '+'
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

func isDelim(r rune) bool {
	return r == lparen || r == rparen
}

func isVariable(r, k rune) bool {
	return r == dollar && k == lcurly
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
	return unicode.IsLetter(r)
}

func isHexa(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isOctal(r rune) bool {
	return r >= '0' && r <= '7'
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isAlpha(r rune) bool {
	return isLetter(r) || unicode.IsDigit(r) || isDigit(r) || r == underscore
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

func isSign(r rune) bool {
	return r == plus || r == minus
}

type recognizer interface {
	transition(rune)
	valid() bool
	typeOf() Type
}

type decimalNumberState uint8

const (
	decimalStateNumber decimalNumberState = iota
	decimalStateUnderscore
	decimalStateSign
	decimalStateZero
	decimalStateFraction
	decimalStateFractionNumber
	decimalStateFractionUnderscore
	decimalStateExponent
	decimalStateExponentNumber
	decimalStateExponentSign
	decimalStateExponentUnderscore
	decimalStateInvalid
)

type decimalNumberRecognizer struct {
	state decimalNumberState
}

func newDecimalNumberRecognizer(start decimalNumberState) recognizer {
	return &decimalNumberRecognizer{
		state: start,
	}
}

func (r *decimalNumberRecognizer) transition(char rune) {
	switch r.state {
	case decimalStateInvalid:
	case decimalStateNumber:
		if char == underscore {
			r.state = decimalStateUnderscore
		} else if char == 'e' || char == 'E' {
			r.state = decimalStateExponent
		} else if char == dot {
			r.state = decimalStateFraction
		} else if !isDigit(char) {
			r.state = decimalStateInvalid
		}
	case decimalStateUnderscore:
		if !isDigit(char) {
			r.state = decimalStateInvalid
		} else {
			r.state = decimalStateNumber
		}
	case decimalStateSign:
		if isDigit(char) && char != '0' {
			r.state = decimalStateNumber
		} else if char == '0' {
			r.state = decimalStateZero
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateZero:
		if char == dot {
			r.state = decimalStateFraction
		} else if char == 'e' || char == 'E' {
			r.state = decimalStateExponent
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateFraction:
		if isDigit(char) {
			r.state = decimalStateFractionNumber
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateFractionNumber:
		if char == underscore {
			r.state = decimalStateFractionUnderscore
		} else if char == 'e' || char == 'E' {
			r.state = decimalStateExponent
		} else if !isDigit(char) {
			r.state = decimalStateInvalid
		}
	case decimalStateFractionUnderscore:
		if isDigit(char) {
			r.state = decimalStateFractionNumber
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateExponent:
		if isSign(char) {
			r.state = decimalStateExponentSign
		} else if isDigit(char) {
			r.state = decimalStateExponentNumber
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateExponentUnderscore:
		if isDigit(char) {
			r.state = decimalStateExponentNumber
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateExponentSign:
		if isDigit(char) {
			r.state = decimalStateExponentNumber
		} else {
			r.state = decimalStateInvalid
		}
	case decimalStateExponentNumber:
		if char == underscore {
			r.state = decimalStateExponentUnderscore
		} else if !isDigit(char) {
			r.state = decimalStateInvalid
		}
	}
}

func (r *decimalNumberRecognizer) valid() bool {
	return r.state == decimalStateNumber ||
		r.state == decimalStateFractionNumber ||
		r.state == decimalStateExponentNumber
}

func (r *decimalNumberRecognizer) typeOf() Type {
	switch r.state {
	case decimalStateNumber:
		return TokInt
	case decimalStateFractionNumber, decimalStateExponentNumber:
		return TokFloat
	default:
		return TokInvalid
	}
}

type baseNumberState uint8

const (
	baseStateNumber baseNumberState = iota
	baseStateUnderscore
	baseStateInvalid
)

type baseNumberRecognizer struct {
	state  baseNumberState
	accept func(rune) bool
}

func newBaseNumberRecognizer(accept func(rune) bool) recognizer {
	return &baseNumberRecognizer{
		state:  baseStateNumber,
		accept: accept,
	}
}

func (r *baseNumberRecognizer) transition(char rune) {
	switch r.state {
	case baseStateInvalid:
	case baseStateUnderscore:
		if r.accept(char) {
			r.state = baseStateNumber
		} else {
			r.state = baseStateInvalid
		}
	case baseStateNumber:
		if char == underscore {
			r.state = baseStateUnderscore
		} else if !r.accept(char) {
			r.state = baseStateInvalid
		}
	}
}

func (r *baseNumberRecognizer) typeOf() Type {
	switch r.state {
	case baseStateNumber:
		return TokInt
	default:
		return TokInvalid
	}
}

func (r *baseNumberRecognizer) valid() bool {
	return r.state == baseStateNumber
}
