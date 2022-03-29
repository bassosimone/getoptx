package main

import (
	"log"
	"os"

	"github.com/bassosimone/getoptx"
)

type RunWebsitesOptions struct {
	EnableHTTP3 bool `doc:"enable HTTP3 measurements"`
}

type RunIMOptions struct {
	TestAllEndpoints bool `doc:"test all available endpoints"`
}

type RunOptions struct {
	Input    []string           `doc:"add URL to measure" short:"i"`
	Websites RunWebsitesOptions `doc:"-"`
	IM       RunIMOptions       `doc:"-"`
}

type ListOptions struct {
	ID int `doc:"ID of the input to show"`
}

type Options struct {
	Batch   bool            `doc:"emit JSON formatted logs" short:"b"`
	Verbose getoptx.Counter `doc:"increases verbosity" short:"v"`
	Run     RunOptions      `doc:"-"`
	List    ListOptions     `doc:"-"`
}

func main() {
	options := &Options{
		Batch:   false,
		Verbose: 0,
		Run: RunOptions{
			Input: []string{},
			Websites: RunWebsitesOptions{
				EnableHTTP3: false,
			},
			IM: RunIMOptions{
				TestAllEndpoints: false,
			},
		},
		List: ListOptions{
			ID: 0,
		},
	}
	cli := getoptx.Command(
		"network measurement tool", options,
		getoptx.Subcommand(
			"run", "runs nettests", &options.Run,
			getoptx.LeafSubcommand(
				"websites", "checks for blocked websites",
				&options.Run.Websites,
				getoptx.NoPositionalArguments(),
			),
			getoptx.LeafSubcommand(
				"im", "checks for blocked IM apps",
				&options.Run.IM,
				getoptx.NoPositionalArguments(),
			),
		),
		getoptx.LeafSubcommand(
			"list", "lists available measurements", &options.List,
			getoptx.NoPositionalArguments(),
		),
	)
	selected := cli.MustGetopt(os.Args)
	switch selected.Options().(type) {
	case *RunWebsitesOptions:
		log.Printf("run websites with: %+v", options)
	case *RunIMOptions:
		log.Printf("run IM with: %+v", options)
	case *ListOptions:
		log.Printf("lists measurements with: %+v", options)
	case *getoptx.HasPrintedHelp:
		os.Exit(1)
	default:
		log.Fatalf("unhandled selected command: %T %+v", selected, selected)
	}
}
