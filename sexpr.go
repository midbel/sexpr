package sexpr

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	ErrSyntax = errors.New("invalid syntax")
	ErrInput  = errors.New("bad input")
)

type Ident string

type Environment interface {
	FileResolver
	Resolver
}

type FileResolver interface {
	Open(string) (io.ReadCloser, error)
}

type Resolver interface {
	Resolve(string) (any, error)
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
		v = envResolver{}
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
		nr envResolver
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

type environment struct {
	Resolver
	FileResolver
}

type envResolver struct{}

func (envResolver) Resolve(ident string) (any, error) {
	return os.Getenv(ident), nil
}

type localFileResolver struct{}

func (localFileResolver) Open(file string) (io.ReadCloser, error) {
	return os.Open(file)
}

type parser struct {
	handle Handler
	env    Environment

	scan *scanner
	curr Token
	peek Token
}

func createParser(r io.Reader, h Handler, v Resolver) (*parser, error) {
	scan, err := createScanner(r)
	if err != nil {
		return nil, err
	}
	var env Environment
	if v, ok := v.(Environment); ok {
		env = v
	} else if _, ok := v.(FileResolver); !ok {
		env = environment{
			Resolver:     v,
			FileResolver: localFileResolver{},
		}
	}
	p := &parser{
		scan:   scan,
		env:    env,
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
	val, err := p.env.Resolve(p.curr.Literal)
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
	if p.is(TokSymbol) && p.curr.Literal == "include" {
		return p.parseInclude()
	}
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

func (p *parser) parseInclude() error {
	p.next()
	for !p.done() && !p.is(TokEndList) {
		if !p.is(TokString) {
			return fmt.Errorf("string expected")
		}
		if err := p.includeFile(p.curr.Literal); err != nil {
			return err
		}
		p.next()
	}
	if !p.is(TokEndList) {
		return fmt.Errorf("expected closing parenthesis at end of list")
	}
	p.next()
	return nil
}

func (p *parser) includeFile(file string) error {
	r, err := p.env.Open(file)
	if err != nil {
		return err
	}
	defer r.Close()

	sub, err := createParser(r, p.handle, p.env)
	if err != nil {
		return err
	}
	return sub.parse()
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

var units = map[string]float64{
	"k":  1000,
	"ko": 1000,
	"kb": 1024,
	"m":  1000 * 1000,
	"mo": 1000 * 1000,
	"mb": 1024 * 1024,
	"g":  1000 * 1000 * 1000,
	"go": 1000 * 1000 * 1000,
	"gb": 1024 * 1024 * 1024,
	"t":  1000 * 1000 * 1000 * 1000,
	"to": 1000 * 1000 * 1000 * 1000,
	"tb": 1024 * 1024 * 1024 * 1024,
}

func splitUnit(val string) (string, float64, error) {
	ix := strings.IndexFunc(val, unicode.IsLetter)
	if ix < 0 {
		return val, 1, nil
	}
	val = val[:ix]
	coeff, ok := units[val[ix:]]
	if !ok {
		return "", 0, fmt.Errorf("%s: unit undefined", val[ix:])
	}
	return val, coeff, nil
}

const defaultIndentSize = 2

type writer struct {
	out     *bufio.Writer
	scan    *scanner
	compact bool
	step    int
	depth   int

	prevLine int
	curr     Token
	peek     Token
}

func createWriter(r io.Reader, w io.Writer) *writer {
	scan, _ := createScanner(r)
	ws := &writer{
		out:  bufio.NewWriter(w),
		scan: scan,
		step: defaultIndentSize,
	}
	ws.next()
	ws.next()

	return ws
}

func (w *writer) Write() error {
	for !w.done() {
		if err := w.writeToken(); err != nil {
			return err
		}
	}
	return w.out.Flush()
}

func (w *writer) writeToken() error {
	var err error
	switch w.curr.Type {
	case TokBegList:
		err = w.enterList()
		w.writeAfterBegList()
	case TokEndList:
		err = w.leaveList()
		w.writeAfterEndList()
	case TokComment:
		if w.compact {
			break
		}
		err = w.writeComment()
		w.writeAfterComment()
	case TokString:
		err = w.writeQuote()
		w.writeAfterAtom()
	case TokDirective:
		if w.compact {
			break
		}
		err = w.writeDirective()
	case TokVariable:
		err = w.writeVariable()
		w.writeAfterAtom()
	case TokInvalid:
	default:
		err = w.writeAtom()
		w.writeAfterAtom()
	}
	return err
}

func (w *writer) enterList() error {
	w.next()
	w.out.WriteRune(lparen)
	w.depth += w.step
	return nil
}

func (w *writer) leaveList() error {
	w.next()
	w.out.WriteRune(rparen)
	w.depth -= w.step
	return nil
}

func (w *writer) writeComment() error {
	defer w.next()
	w.out.WriteRune(semicolon)
	w.out.WriteRune(space)
	_, err := w.out.WriteString(w.curr.Literal)
	return err
}

func (w *writer) writeVariable() error {
	defer w.next()

	w.out.WriteRune(dollar)
	w.out.WriteRune(lcurly)
	_, err := w.out.WriteString(w.curr.Literal)
	w.out.WriteRune(rcurly)
	return err
}

func (w *writer) writeDirective() error {
	defer w.next()

	w.out.WriteRune(pound)
	w.out.WriteRune(bang)
	w.out.WriteRune(space)
	_, err := w.out.WriteString(w.curr.Literal)
	return err
}

func (w *writer) writeAtom() error {
	defer w.next()
	return w.writeString(w.curr.Literal)
}

func (w *writer) writeQuote() error {
	w.out.WriteRune(quote)
	_, err := w.out.WriteString(w.curr.Literal)
	w.out.WriteRune(quote)
	w.next()
	w.writeAfterAtom()
	return err
}

func (w *writer) writeString(str string) error {
	_, err := w.out.WriteString(str)
	return err
}

func (w *writer) writeSpace() error {
	_, err := w.out.WriteRune(space)
	return err
}

func (w *writer) writeNL() error {
	char := nl
	if w.compact {
		char = space
	}
	_, err := w.out.WriteRune(char)
	return err
}

func (w *writer) writeIndent() {
	if w.compact {
		return
	}
	for range w.depth {
		w.writeSpace()
	}
}

func (w *writer) writeAfterComment() {
	if w.curr.Type == TokEndList {
		w.depth--
	}
	w.writeNL()
	w.writeIndent()
}

func (w *writer) writeAfterBegList() {
	switch {
	case w.curr.Type.Atom():
	case w.curr.Type == TokEndList:
	case w.curr.Type == TokBegList || w.curr.Type == TokComment:
		w.writeNL()
		w.writeIndent()
	}
}

func (w *writer) writeAfterEndList() {
	switch {
	case w.curr.Type.Atom():
		w.writeSpace()
	case w.curr.Type.Comment():
		if w.prevLine == w.curr.Line {
			w.writeSpace()
		} else {
			w.writeNL()
			w.writeIndent()
		}
	case w.curr.Type == TokBegList:
		w.writeNL()
		w.writeIndent()
	case w.curr.Type == TokEndList:
	}
}

func (w *writer) writeAfterAtom() {
	switch {
	case w.curr.Type.Atom():
		w.writeSpace()
	case w.curr.Type.Comment():
		if w.prevLine == w.curr.Line {
			w.curr.Type.Comment()
		} else {
			w.writeNL()
			w.writeIndent()
		}
	case w.curr.Type == TokBegList:
		w.writeNL()
		w.writeIndent()
	case w.curr.Type == TokEndList:
	}
}

func (w *writer) next() {
	w.prevLine = w.curr.Line

	w.curr = w.peek
	w.peek = w.scan.Scan()
}

func (w *writer) done() bool {
	return w.curr.Type == TokEof
}

func Format(r io.Reader, w io.Writer, compact bool) error {
	ws := createWriter(r, w)
	ws.compact = compact
	return ws.Write()
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

type ScannerContext struct {
	File string
	Line string
	Position
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

func (t Type) Comment() bool {
	return t == TokComment
}

func (t Type) Atom() bool {
	switch t {
	case TokSymbol:
	case TokString:
	case TokFloat:
	case TokInt:
	case TokBoolean:
	case TokVariable:
	case TokDate:
	case TokDateTime:
	default:
		return false
	}
	return true
}

type scanner struct {
	file    string
	input   *bufio.Reader
	err     error
	char    rune
	history *ring

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
			if tok.Type == TokEof || errors.Is(scan.Err(), io.EOF) {
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
		file:    "stream",
		input:   input,
		buf:     new(bytes.Buffer),
		history: newRing(64),
	}
	if n, ok := r.(interface{ Name() string }); ok {
		s.file = n.Name()
	}
	s.Position.Line++
	s.advance()
	return s, nil
}

func (s *scanner) Context() ScannerContext {
	peek, _ := s.input.Peek(32)
	ctx := ScannerContext{
		File:     s.file,
		Position: s.Position,
		Line:     s.history.String() + string(peek),
	}
	return ctx
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
	case isSign(s.char) || isDigit(s.char):
		s.scanNumber(&tok)
	case isDirective(s.char, s.peek()):
		s.scanDirective(&tok)
	case isVariable(s.char, s.peek()):
		s.scanVariable(&tok)
	default:
		tok.Type = TokInvalid
		s.write()
	}
	if tok.Type == TokInvalid {
		s.err = ErrInput
		tok.Literal = s.literal()
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
		case 'b', 'B':
			s.scanBinary(tok)
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
	for !s.done() && reco.can() {
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
	for !s.done() && reco.can() {
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

func (s *scanner) scanBinary(tok *Token) {
	s.write()
	s.advance()
	s.write()
	s.advance()
	if !isBinary(s.char) {
		tok.Type = TokInvalid
		return
	}
	reco := newBaseNumberRecognizer(isBinary)
	for !s.done() && reco.can() {
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
	start := decimalStateNumber
	if isSign(s.char) {
		s.write()
		s.advance()
		start = decimalStateSign
	} else if s.char == '0' {
		s.write()
		s.advance()
		start = decimalStateZero
	}
	until := func(char rune) bool {
		return isBlank(char) || isComment(char) || isDelim(char)
	}
	reco := newDecimalNumberRecognizer(start)
	for !s.done() && !until(s.char) && reco.can() {
		reco.transition(s.char)
		if s.char != underscore {
			s.write()
		}
		s.advance()
	}
	if !reco.can() && !s.done() {
		s.scanDateTime(tok)
		return
	}
	for !s.done() && isLetter(s.char) {
		s.write()
		s.advance()
	}
	tok.Type = reco.typeOf()
	tok.Literal = s.literal()
}

func (s *scanner) scanDateTime(tok *Token) {
	until := func(char rune) bool {
		return isBlank(char) || isComment(char) || isDelim(char)
	}
	reco := newDateTimeRecognizer(dateTimeStateMonth)
	for !s.done() && !until(s.char) && reco.can() {
		reco.transition(s.char)
		s.write()
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
		s.char = 0
		return
	}
	s.char = c
	s.history.Put(s.char)
	if s.char == cr && s.peek() == nl {
		s.char, _, _ = s.input.ReadRune()
		s.history.Put(s.char)
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
	return errors.Is(s.err, io.EOF) || s.char == 0
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

type ring struct {
	runes  []rune
	offset int
	count  int
}

func newRing(size int) *ring {
	return &ring{
		runes:  make([]rune, size),
		offset: 0,
	}
}

func (r *ring) String() string {
	if r.offset == len(r.runes) || r.offset == 0 {
		return string(r.runes)
	}
	if r.count < len(r.runes) {
		return string(r.runes[:r.count])
	}
	return string(r.runes[r.offset:]) + string(r.runes[:r.offset])
}

func (r *ring) Put(char rune) {
	r.runes[r.offset] = char
	r.offset = (r.offset + 1) % len(r.runes)
	if r.count < len(r.runes) {
		r.count++
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
	colon      = ':'
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

func isBinary(r rune) bool {
	return r == '0' || r == '1'
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
	can() bool
	typeOf() Type
}

type dateTimeState uint8

const (
	dateTimeStateYear dateTimeState = iota
	dateTimeStateYearNumber
	dateTimeStateMonth
	dateTimeStateMonthNumber
	dateTimeStateDay
	dateTimeStateDayNumber
	dateTimeStateHour
	dateTimeStateHourNumber
	dateTimeStateMinute
	dateTimeStateMinuteNumber
	dateTimeStateSecond
	dateTimeStateSecondNumber
	dateTimeStateMillis
	dateTimeStateMillisNumber
	dateTimeStateUTC
	dateTimeStateOffset
	dateTimeStateOffsetHourNumber
	dateTimeStateOffsetMinute
	dateTimeStateOffsetMinuteNumber
	dateTimeStateInvalid
)

type dateTimeRecognizer struct {
	state dateTimeState
}

func newDateTimeRecognizer(start dateTimeState) recognizer {
	return &dateTimeRecognizer{
		state: start,
	}
}

func (r *dateTimeRecognizer) can() bool {
	return r.state != dateTimeStateInvalid
}

func (r *dateTimeRecognizer) transition(char rune) {
	switch r.state {
	case dateTimeStateInvalid:
	case dateTimeStateYear:
		if isDigit(char) {
			r.state = dateTimeStateYearNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateYearNumber:
		if char == minus {
			r.state = dateTimeStateMonth
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateMonth:
		if isDigit(char) {
			r.state = dateTimeStateMonthNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateMonthNumber:
		if char == minus {
			r.state = dateTimeStateDay
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateDay:
		if isDigit(char) {
			r.state = dateTimeStateDayNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateDayNumber:
		if char == 'T' {
			r.state = dateTimeStateHour
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateHour:
		if isDigit(char) {
			r.state = dateTimeStateHourNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateHourNumber:
		if char == colon {
			r.state = dateTimeStateMinute
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateMinute:
		if isDigit(char) {
			r.state = dateTimeStateMinuteNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateMinuteNumber:
		if char == colon {
			r.state = dateTimeStateSecond
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateSecond:
		if isDigit(char) {
			r.state = dateTimeStateSecondNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateSecondNumber:
		if char == dot {
			r.state = dateTimeStateMillis
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateMillis:
		if isDigit(char) {
			r.state = dateTimeStateMillisNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateMillisNumber:
		if char == 'Z' {
			r.state = dateTimeStateUTC
		} else if isSign(char) {
			r.state = dateTimeStateOffset
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateUTC:
		r.state = dateTimeStateInvalid
	case dateTimeStateOffset:
		if isDigit(char) {
			r.state = dateTimeStateOffsetHourNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateOffsetHourNumber:
		if char == colon {
			r.state = dateTimeStateOffsetMinute
		} else if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateOffsetMinute:
		if isDigit(char) {
			r.state = dateTimeStateOffsetMinuteNumber
		} else {
			r.state = dateTimeStateInvalid
		}
	case dateTimeStateOffsetMinuteNumber:
		if !isDigit(char) {
			r.state = dateTimeStateInvalid
		}
	}
}

func (r *dateTimeRecognizer) valid() bool {
	return r.state == dateTimeStateDayNumber ||
		r.state == dateTimeStateMinuteNumber ||
		r.state == dateTimeStateSecondNumber ||
		r.state == dateTimeStateMillisNumber ||
		r.state == dateTimeStateUTC ||
		r.state == dateTimeStateOffsetMinuteNumber
}

func (r *dateTimeRecognizer) typeOf() Type {
	switch r.state {
	default:
		return TokInvalid
	case dateTimeStateDayNumber:
		return TokDate
	case dateTimeStateMinuteNumber,
		dateTimeStateSecondNumber,
		dateTimeStateMillisNumber,
		dateTimeStateUTC,
		dateTimeStateOffsetMinuteNumber:
		return TokDateTime
	}
	return 0
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

func (r *decimalNumberRecognizer) can() bool {
	return r.state != decimalStateInvalid
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
		r.state == decimalStateZero ||
		r.state == decimalStateFractionNumber ||
		r.state == decimalStateExponentNumber
}

func (r *decimalNumberRecognizer) typeOf() Type {
	switch r.state {
	case decimalStateNumber, decimalStateZero:
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

func (r *baseNumberRecognizer) can() bool {
	return r.state != baseStateInvalid
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
