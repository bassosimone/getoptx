package main

import (
	"fmt"
	"os"

	"github.com/bassosimone/getoptx"
)

type Options struct {
	Batch   bool            `doc:"emit JSON formatted logs" short:"b"`
	Input   []string        `doc:"add URL to measure" short:"i"`
	Verbose getoptx.Counter `doc:"increases verbosity" short:"v"`
}

func main() {
	options := &Options{
		Batch:   false,
		Input:   []string{},
		Verbose: 0,
	}
	parser := getoptx.MustNewParser(options)
	parser.MustGetopt(os.Args)
	fmt.Printf("options: %+v\n", options)
	fmt.Printf("args   : %+v\n", parser.Args())
}
