package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/midbel/cli"
	"github.com/midbel/sexpr"
)

func main() {
	flag.Parse()
	r, err := os.Open(flag.Arg(0))
	if err != nil {
		cli.FailIO(err)
	}
	defer r.Close()

	expr, err := sexpr.Decode(r)
	if err != nil {
		cli.FailData(err)
	}
	fmt.Println(expr)
}
