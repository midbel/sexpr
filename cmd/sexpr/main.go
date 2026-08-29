package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/midbel/cli"
	"github.com/midbel/sexpr"
)

var errFail = errors.New("fail")

func main() {
	var (
		set  = cli.NewFlagSet("trellis")
		root = prepare()
	)
	if err := set.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			root.Help()
			os.Exit(2)
		}
	}
	err := root.Execute(set.Args())
	if err != nil {
		if s, ok := err.(cli.SuggestionError); ok && len(s.Others) > 0 {
			fmt.Fprintln(os.Stderr, "similar command(s)")
			for _, n := range s.Others {
				fmt.Fprintln(os.Stderr, "-", n)
			}
		}
		if !errors.Is(err, errFail) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

var statsCmd = cli.Command{
	Name:    "stats",
	Summary: "",
	Usage:   "stats <file>",
	Handler: &statsCommand{},
}

type statsCommand struct{}

func (c statsCommand) Run(args []string) error {
	set := cli.NewFlagSet("stats")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return cli.ErrUsage
	}
	r, err := os.Open(set.Arg(0))
	if err != nil {
		cli.FailIO(err)
	}
	stat := sexpr.Stats(r)
	tbl := cli.Table{
		Headers: []string{"Type", "Count"},
		Rows: [][]string{
			{"Identifiers", strconv.Itoa(stat.Idents)},
			{"Strings", strconv.Itoa(stat.Strings)},
			{"Integers", strconv.Itoa(stat.Ints)},
			{"Floats", strconv.Itoa(stat.Floats)},
			{"Booleans", strconv.Itoa(stat.Bools)},
			{"Lists", strconv.Itoa(stat.Lists)},
			{"Variables", strconv.Itoa(stat.Vars)},
			{"Directives", strconv.Itoa(stat.Directives)},
			{"Comments", strconv.Itoa(stat.Comments)},
		},
	}
	rdr := cli.NewTableRenderer(cli.Stdout)
	rdr.Render(tbl)
	return nil
}

var scanCmd = cli.Command{
	Name:    "scan",
	Summary: "",
	Usage:   "scan <file>",
	Handler: &scanCommand{},
}

type scanCommand struct{}

func (c scanCommand) Run(args []string) error {
	set := cli.NewFlagSet("scan")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return cli.ErrUsage
	}
	r, err := os.Open(set.Arg(0))
	if err != nil {
		cli.FailIO(err)
	}
	it, err := sexpr.Lex(r)
	if err != nil {
		return err
	}
	var ix int
	for tok, err := range it {
		if err != nil {
			return err
		}
		ix++
		fmt.Fprintf(cli.Stdout, "%03d | %5s | %12s | %s", ix, tok.Position, tok.Type, tok.Literal)
		fmt.Fprintln(cli.Stdout)
	}
	return nil
}

func prepare() *cli.CommandTrie {
	root := cli.New()
	root.Register(single("scan"), &scanCmd)
	root.Register(single("stats"), &statsCmd)
	return root
}

func single(str string) []string {
	return []string{str}
}
