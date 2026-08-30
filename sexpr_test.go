package sexpr

import (
	"strings"
	"testing"
)

func TestScannerAtoms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		typ   Type
		value string
	}{
		// Symbols
		{
			name:  "symbol",
			input: "foo",
			typ:   TokSymbol,
			value: "foo",
		},
		{
			name:  "symbol with dash",
			input: "foo-bar",
			typ:   TokSymbol,
			value: "foo-bar",
		},
		{
			name:  "symbol with underscore",
			input: "foo_bar",
			typ:   TokSymbol,
			value: "foo_bar",
		},
		// Boolean
		{
			name:  "true",
			input: "true",
			typ:   TokBoolean,
			value: "true",
		},
		{
			name:  "false",
			input: "false",
			typ:   TokBoolean,
			value: "false",
		},

		// Strings
		{
			name:  "string",
			input: `"hello"`,
			typ:   TokString,
			value: "hello",
		},
		{
			name:  "empty string",
			input: `""`,
			typ:   TokString,
			value: "",
		},
		{
			name:  "escaped quote",
			input: `"hello \"world\""`,
			typ:   TokString,
			value: `hello "world"`,
		},
		{
			name:  "escaped backslash",
			input: `"foo\\bar"`,
			typ:   TokString,
			value: `foo\bar`,
		},
		{
			name:  "escaped newline",
			input: `"foo\nbar"`,
			typ:   TokString,
			value: "foo\nbar",
		},
		{
			name:  "escaped carriage return",
			input: `"foo\rbar"`,
			typ:   TokString,
			value: "foo\rbar",
		},
		{
			name:  "escaped tab",
			input: `"foo\tbar"`,
			typ:   TokString,
			value: "foo\tbar",
		},
		// Integers
		{
			name:  "zero",
			input: "0",
			typ:   TokInt,
			value: "0",
		},
		{
			name:  "integer",
			input: "123",
			typ:   TokInt,
			value: "123",
		},
		{
			name:  "negative integer",
			input: "-123",
			typ:   TokInt,
			value: "-123",
		},
		{
			name:  "positive integer",
			input: "+123",
			typ:   TokInt,
			value: "+123",
		},
		{
			name:  "integer separator",
			input: "1_000_000",
			typ:   TokInt,
			value: "1000000",
		},
		// Hexadecimal
		{
			name:  "hexadecimal",
			input: "0xFF",
			typ:   TokInt,
			value: "0xFF",
		},
		{
			name:  "hexadecimal lowercase",
			input: "0xff",
			typ:   TokInt,
			value: "0xff",
		},
		{
			name:  "hexadecimal separator",
			input: "0xFF_FF",
			typ:   TokInt,
			value: "0xFFFF",
		},
		// Octal
		{
			name:  "octal",
			input: "0o755",
			typ:   TokInt,
			value: "0o755",
		},
		{
			name:  "octal separator",
			input: "0o7_55",
			typ:   TokInt,
			value: "0o755",
		},
		// Floats
		{
			name:  "fraction",
			input: "12.34",
			typ:   TokFloat,
			value: "12.34",
		},
		{
			name:  "negative fraction",
			input: "-12.34",
			typ:   TokFloat,
			value: "-12.34",
		},
		{
			name:  "fraction separator",
			input: "12.345_678",
			typ:   TokFloat,
			value: "12.345678",
		},
		// Exponents
		{
			name:  "exponent",
			input: "12e3",
			typ:   TokFloat,
			value: "12e3",
		},
		{
			name:  "negative exponent",
			input: "12e-3",
			typ:   TokFloat,
			value: "12e-3",
		},
		{
			name:  "positive exponent",
			input: "12e+3",
			typ:   TokFloat,
			value: "12e+3",
		},
		{
			name:  "exponent separator",
			input: "12e123_456",
			typ:   TokFloat,
			value: "12e123456",
		},
		{
			name:  "fraction exponent",
			input: "12.34e5",
			typ:   TokFloat,
			value: "12.34e5",
		},
		// Directive
		{
			name:  "directive",
			input: "#!foo",
			typ:   TokDirective,
			value: "",
		},
		// Variable
		{
			name:  "variable",
			input: "${foo}",
			typ:   TokVariable,
			value: "foo",
		},
		// Comment
		{
			name:  "comment",
			input: "; hello world",
			typ:   TokComment,
			value: "hello world",
		},
		// Delimiters
		{
			name:  "open list",
			input: "(",
			typ:   TokBegList,
			value: "",
		},
		{
			name:  "close list",
			input: ")",
			typ:   TokEndList,
			value: "",
		},
		// Invalid
		{
			name:  "invalid character",
			input: "@",
			typ:   TokInvalid,
			value: "@",
		},
		{
			name:  "unterminated string",
			input: `"hello`,
			typ:   TokInvalid,
			value: "hello",
		},
		{
			name:  "invalid escape",
			input: `"hello\q"`,
			typ:   TokInvalid,
			value: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scan, err := createScanner(strings.NewReader(tt.input))
			if err != nil {
				t.Fatal(err)
			}

			tok := scan.Scan()

			if tok.Type != tt.typ {
				t.Errorf("type: got %s, want %s", tok.Type, tt.typ)
			}
			if tok.Literal != tt.value {
				t.Errorf("literal: got %q, want %q", tok.Literal, tt.value)
			}
		})
	}
}
