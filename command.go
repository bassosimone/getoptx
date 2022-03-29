package getoptx

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/mitchellh/go-wordwrap"
)

// HasPrintedHelp is the fake subcommand returned when CommandParser.Getopt or
// CommandParser.MustGetopt have printed an help message.
type HasPrintedHelp struct{}

// Command creates the toplevel command for the whole program with the given
// description, the given options, and zero or more subcommands. This function
// also register an internal subcommand implementing the `help` subcommand
// unless you have already included one such command in subcommands. Apart from
// that, this function is equivalent to calling Subcommand with os.Args[0] as
// the first argument followed by the arguments passed to Command. For this
// reason, please see Subcommand for more information about the usage.
func Command(
	description string, options interface{}, subcommands ...*CommandParser) *CommandParser {
	if !containsHelp(subcommands) {
		subcommands = append(subcommands, LeafSubcommand(
			"help", "Prints generic or command-specific help", &subcommandHelp{}))
	}
	return Subcommand(os.Args[0], description, options, subcommands...)
}

func containsHelp(subcommands []*CommandParser) bool {
	for _, sc := range subcommands {
		if sc.name == "help" {
			return true
		}
	}
	return false
}

// subcommandHelp is the internal "help" subcommand.
type subcommandHelp struct{}

// Subcommand creates a new subcommand with the given name, the given description,
// the given options, and zero or more subcommands.
//
// The typical usage for Command, Subcommand, and LeafSubcommand calls for
// creating data structures containing options you'd like to fill and for
// instantiating a complex command line parser by chaining calls to Command,
// Subcommand, or LeafSubcommand depending on your needs.
//
// Here's an example:
//
//     type GlobalOptions struct {
//       Batch     bool     `doc:"emit JSON messages" short:"b"`
//       Logfile   string   `doc:"file where to write logs" short:"L"`
//       Verbose   bool     `doc:"run in verbose mode" short:"v"`
//     }
//
//     type WebsitesOptions struct {
//       ForceHTTP3 bool `doc:"forces using HTTP3" short:"3"`
//     }
//
//     type URLGetterOptions struct {
//       SNI  string `doc:"forces using a specific SNI"`
//       Host string `doc:"forces using a specific host header"`
//     }
//
//     type RunOptions struct {
//       Input     string           `doc:"add URL to measure"`
//       InputFile string           `doc:"add file from which to read URLs"`
//       Websites  WebsitesOptions  `doc:"-"`  // <- (3)
//       URLGetter URLGetterOptions `doc:"-"`
//     }
//
//     type ListOptions struct {
//       ID int `doc:"ID of the result to show" short:"I"`
//     }
//
//     type RmOptions struct {
//       ID int `doc:"ID of the result to remove" short:"I"`
//     }
//
//     type Options struct {
//       Global GlobalOptions
//       Run    RunOptions
//       Rm     RmOptions
//       List   ListOptions
//     }
//
//     options := &Options{}
//     cli := getoptx.Command(
//       "Network measurement tool",
//       &options.Global,
//       getoptx.Subcommand(
//         "run", "Runs network measurements", &options.RunOptions,
//         getoptx.LeafCommand(
//           "websites", "Tests websites for censorship", &options.Websites,
//           getoptx.NoPositionalArguments(),
//         ),
//         getoptx.LeafCommand(
//           "urlgetter", "Swiss-knife measurements tool", &options.URLGetter,
//           getoptx.NoPositionalArguments(),
//         ),
//       ),
//       getoptx.LeafCommand(
//         "list", "Lists network measurements", &options.ListOptions,
//       ),
//       getoptx.LeafCommand(
//         "rm", "Remove network measurements", &options.RmOptions,
//       ),
//     )
//
//     selected := cli.MustGetopt(os.Args)
//     switch selected.Options().(type) { // <- (2)
//     case *WebsitesOptions:
//       Websites(options)
//     case *URLGetterOptions:
//       URLGetter(options)
//     case *ListOptions:
//       List(options)
//     case *RmOptions:
//       Rm(options)
//     case *getoptx.HasPrintedHelp:  // <- (1)
//       os.Exit(0)
//     default:
//       log.Fatalf("unhandled selected option: %T %+v", selected, selected)
//     }
//
// This example shows the most complex usage possible. In particular, here you
// see the following functionality in action:
//
// 1. MustGetopt automatically prints contextual help for -h/--help as well
// as for the `help` subcommand and lets you know when this happens;
//
// 2. you know which command has been selected by checking for the type of
// the leaf option structure that has been filled;
//
// 3. you can embed options within other options and mark them as `doc:"-"` to
// force the underlying parser to skip them (since they can't be parsed).
//
// If you want to write a custom `"help"` command, you just need to pass to
// the toplevel Command call a subcommand implementing `"help"`. In which
// case, we will not register our internal interceptor for the `"help"` command.
//
// Likewise, we'll attach to each subparser a -h/--help rule for printing
// help. But you can avoid this by adding an option that resolves to -h or --help.
func Subcommand(name, description string, options interface{},
	subcommands ...*CommandParser) *CommandParser {
	sort.SliceStable(subcommands, func(i, j int) bool { // ensure subcommands are sorted
		return subcommands[i].name < subcommands[j].name
	})
	return &CommandParser{
		description: description,
		help:        false,
		name:        name,
		options:     options,
		pac:         newPositionalArgumentsChecker(),
		subcommands: subcommands,
	}
}

// LeafSubcommand creates a subcommand that does not take any further
// subcommand (i.e., a "leaf" subcommand in the commands tree).
//
// You can use Configs such as NoPositionalArguments() to control the leaf
// subcommand behavior in terms of positional arguments. This function will
// emit a warning and otherwise ignore any piece of config that does not
// specifically deal with controlling positional arguments.
//
// See Subcommand's docs for further information.
func LeafSubcommand(
	name, description string, options interface{}, config ...Config) *CommandParser {
	p := Subcommand(name, description, options)
	for _, entry := range config {
		switch value := entry.(type) {
		case *minMaxPositionalArguments:
			p.pac.minArgs = value.minArgs
			p.pac.maxArgs = value.maxArgs
		default:
			log.Printf("getoptx: ignoring unsupported piece of config: %T %+v", entry, entry)
		}
	}
	return p
}

// CommandParser is a parser for a command or a subcommand. You construct this
// type using Command (for a top-level command) or Subcommand.
//
// Once you have constructed a CommandParser you call Getopt or MustGetopt
// to parse the command line arguments and get the SelectedSubcommand.
//
// See Subcommand for more details on the typical usage.
type CommandParser struct {
	// description is the command description.
	description string

	// help allows registering and using -h/--help.
	help bool

	// name is the command name.
	name string

	// options are the command options.
	options interface{}

	// pac is the positional arguments checker.
	pac *positionalArgumentsChecker

	// subcommands contains the subcommands.
	subcommands []*CommandParser
}

// SelectedCommand is the type returned by successful parsing of command
// line arguments by CommandParser.{Must,}Getopt.
//
// See the documentation of Subcommand for more info on actual usage.
type SelectedCommand struct {
	// options are the options.
	options interface{}

	// args contains the positional arguments.
	args []string
}

// Args returns the selected command's positional arguments.
func (sc *SelectedCommand) Args() []string {
	return sc.args
}

// NArgs returns the number of positional arguments.
func (sc *SelectedCommand) NArgs() int {
	return len(sc.args)
}

// Options returns the selected command's options. See the documentation
// of Subcommand for more information on how to use this method.
func (sc *SelectedCommand) Options() interface{} {
	return sc.options
}

// Getopt parses command line arguments for the given command. This function returns
// either the selected command or subcommand, or an error.
//
// See the documentation of the Subcommand factory for more usage information.
func (p *CommandParser) Getopt(args []string) (*SelectedCommand, error) {
	if len(args) < 1 {
		return nil, errors.New("passed a zero length argv")
	}
	sc, err := p.getoptall([]*CommandParser{p}, args)
	if err != nil {
		return nil, err
	}
	// Intercept the case where our internal "help" subcommand was selected
	// and implement it by basically transforming this command line
	//
	//     args0 help a b c
	//
	// into
	//
	//     args0 a b c --help
	//
	// which will invoke contextual help for `a b c`.
	if _, okay := sc.options.(*subcommandHelp); okay {
		v := append([]string{args[0]}, sc.args...)
		v = append(v, "--help")
		return p.Getopt(v)
	}
	return sc, nil
}

// MustGetopt is exactly like Getopt except that it calls os.Exit(1) in case of error.
func (p *CommandParser) MustGetopt(args []string) *SelectedCommand {
	sc, err := p.Getopt(args)
	if err != nil {
		os.Exit(1)
	}
	return sc
}

// ErrNoSuchSubcommand indicates that we don't know a subcommand with that name.
var ErrNoSuchSubcommand = errors.New("no such subcommand")

// getoptall is the internal worker for Getopt.
func (p *CommandParser) getoptall(chain []*CommandParser, args []string) (*SelectedCommand, error) {

	// 0. obtain the command name and crash badly if we have an empty chain
	if len(chain) < 1 {
		panic("called with zero length chain")
	}
	cmd := chain[0].name

	// 1. construct a new parser wrapper with additional support for -h/--help.
	parser, fullcmd, err := p.newParserWrapper(chain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: internal error: %s\n", cmd, err.Error())
		return nil, err
	}

	// 2. parse command line options using the parser.
	if err := parser.Getopt(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s. See '%s --help'.\n", cmd, err.Error(), fullcmd)
		return nil, err
	}

	// 3. handle the special case of -h/--help.
	if p.help {
		p.printHelp(parser, os.Stdout, chain)
		return &SelectedCommand{options: &HasPrintedHelp{}, args: nil}, nil
	}

	// 4. if there are no subcommands left we've reached a leaf. Check whether there are
	// any restrictions regarding positional line arguments and otherwise return the selected
	// command with the positional arguments.
	if len(p.subcommands) <= 0 {
		if err := p.pac.check(parser); err != nil {
			return nil, fmt.Errorf("%s: for command %s: %w", cmd, p.name, err)
		}
		return p.newSelectedCommand(parser.Args()), nil
	}

	// 5. if we expected a subcommand and we didn't find one, then we need to print
	// a message to the user. As a special case, `./program` should always emit
	// the help message that you would see with `./program --help`. This specific
	// choice is quite opinionated but also makes the program more friendly.
	if parser.NArgs() <= 0 {
		if len(chain) < 2 { // this means we're at toplevel
			p.printHelp(parser, os.Stdout, chain)
			return &SelectedCommand{options: &HasPrintedHelp{}, args: nil}, nil
		}
		fmt.Fprintf(os.Stderr,
			"%s: expected subcommand name. See '%s --help'.\n", cmd, fullcmd)
		return nil, errors.New("expected subcommand name")
	}

	// 6. select a subcommand to dispatch to.
	subcmd := parser.Args()[0]
	for _, sc := range p.subcommands {
		if subcmd != sc.name {
			continue // not the command we're looking for
		}
		subchain := append([]*CommandParser{}, chain...)
		subchain = append(subchain, sc)
		return sc.getoptall(subchain, parser.Args())
	}

	// 7. okay we have not found a subcommand, tell the user about this.
	fmt.Fprintf(os.Stderr,
		"%s: no such subcommand: '%s'. See '%s --help'.\n", cmd, subcmd, fullcmd)
	return nil, ErrNoSuchSubcommand
}

// newParserWrapper creates a new parser wrapper. This function also ensures
// that we attach to the parser support for the -h/--help switch if needed.
//
// On success we return a valid parserWrapper, the valid full command we're at, and a
// nil error. On failure, instead, we return nil, the full command, and an error.
func (p *CommandParser) newParserWrapper(chain []*CommandParser) (*parserWrapper, string, error) {
	fullcmd := p.fullcmd(chain)
	var config []Config
	config = append(config, SetPositionalArgumentsPlaceholder(p.positionalArgumentsPlaceholder()))
	config = append(config, SetProgramName(fullcmd))
	parser, err := newParserWrapper(p.options, config...)
	if err != nil {
		return nil, fullcmd, err
	}
	parser.maybeAddHelpFlags(&p.help)
	return parser, fullcmd, nil
}

// newSelectedCommand creates a new instance of SelectedCommand from this CommandParser
// and the current set of positional arguments for the subcommand.
func (p *CommandParser) newSelectedCommand(args []string) *SelectedCommand {
	return &SelectedCommand{
		options: p.options,
		args:    args,
	}
}

// printHelp prints the help message.
func (p *CommandParser) printHelp(
	parser *parserWrapper, w io.Writer, chain []*CommandParser) {
	p.printBriefUsage(w, chain)
	p.printSubcommandDescription(w)
	p.printOptions(w, chain)
	p.printSubcommands(w, nil)
}

// printBriefUsage prints brief usage for this command parser.
func (p *CommandParser) printBriefUsage(w io.Writer, chain []*CommandParser) {
	var sb strings.Builder
	sb.WriteString("\nUsage:")
	for idx, entry := range chain {
		sb.WriteString(" ")
		sb.WriteString(entry.name)
		if entry.hasOptions() {
			sb.WriteString(" [options]")
		}
		if idx >= len(chain)-1 {
			break
		}
	}
	sb.WriteString(p.positionalArgumentsPlaceholder())
	sb.WriteString("\n")
	fmt.Fprint(w, sb.String())
}

func (p *CommandParser) positionalArgumentsPlaceholder() string {
	switch {
	case len(p.subcommands) > 0:
		return " <subcommand> [...]"
	case p.pac.maxArgs > 1:
		return " <argument> [<argument> ...]"
	case p.pac.maxArgs > 0:
		return " <argument>"
	default:
		return ""
	}
}

func (p *CommandParser) hasOptions() bool {
	// TODO(bassosimone): should we warn here in case of error?
	parser, err := newParserWrapper(p.options)
	return err == nil && parser.numOptions() > 0
}

// printSubcommandDescription prints the command's description.
func (p *CommandParser) printSubcommandDescription(w io.Writer) {
	fmt.Fprintf(w, "\n")
	doc := p.description
	if !strings.HasSuffix(doc, ".") {
		doc += "."
	}
	for _, line := range strings.Split(wordwrap.WrapString(doc, 72), "\n") {
		fmt.Fprintf(w, "%s\n", line)
	}
	fmt.Fprintf(w, "\n")
}

// printOptions prints the options up to this point in the chain.
func (p *CommandParser) printOptions(w io.Writer, chain []*CommandParser) {
	for _, entry := range chain {
		parser, err := newParserWrapper(entry.options)
		if err != nil {
			// TODO(bassosimone): should we log this error?!
			continue
		}
		if parser.numOptions() <= 0 {
			continue
		}
		fmt.Fprintf(w, "Options for %s:\n\n", entry.name)
		parser.printOptions(w)
	}
}

// printSubcommands prints the subcommands recursively.
func (p *CommandParser) printSubcommands(w io.Writer, names []string) {
	if len(p.subcommands) > 0 {
		if len(names) <= 0 { // we need to print this only at the beginning
			fmt.Fprintf(w, "Subcommands:\n\n")
		}
		for _, sc := range p.subcommands {
			newnames := append([]string{}, names...)
			newnames = append(newnames, sc.name)
			if len(sc.subcommands) > 0 {
				sc.printSubcommands(w, newnames)
				continue
			}
			p.printSingleSubcommand(w, sc.description, newnames)
		}
	}
}

// printSingleCommand is an utility function for printing help for a single command
func (p *CommandParser) printSingleSubcommand(w io.Writer, doc string, names []string) {
	fmt.Fprintf(w, "  %s\n", strings.Join(names, " "))
	if !strings.HasSuffix(doc, ".") {
		doc += "."
	}
	for _, line := range strings.Split(wordwrap.WrapString(doc, 64), "\n") {
		fmt.Fprintf(w, "             %s\n", line)
	}
	fmt.Fprintf(w, "\n")
}

// fullcmd returns the full command up to this point.
func (p *CommandParser) fullcmd(chain []*CommandParser) string {
	var sequence []string
	for _, pp := range chain {
		sequence = append(sequence, pp.name)
	}
	return strings.Join(sequence, " ")
}
