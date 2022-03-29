package getoptx

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/iancoleman/strcase"
	"github.com/mitchellh/go-wordwrap"
	"github.com/pborman/getopt/v2"
)

// Parser is a command line parser.
type Parser interface {
	// Getopt parses the command line options in args, which in the
	// common case should be just os.Args.
	Getopt(args []string) error

	// MustGetopt is like Getopt but prints usage and exits on error.
	MustGetopt(args []string)

	// PrintUsage prints a detailed usage string on the given writer.
	PrintUsage(w io.Writer)

	// NArgs returns the number of positional arguments.
	NArgs() int

	// Args returns the positional arguments.
	Args() []string
}

// Config is a piece of configuration for NewParser. You can pass
// a bunch of configs to NewParser to change its behavior.
type Config interface {
	visit(p *parserWrapper)
}

// MustNewParser is like NewParser but print an error on stderr
// and calls os.Exit(1) if NewParser fails.
func MustNewParser(flags interface{}, configs ...Config) Parser {
	parser, err := NewParser(flags, configs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		os.Exit(1)
	}
	return parser
}

// NewParser creates a new parser that stores the parsed options into the
// given `flags` opaque argument. Use `config...` to adjust the parser's behavior.
//
// The `flags`` opaque argument must be a pointer to a struct where each
// field must be tagged with `doc:"..."`. We will use the value of the doc
// tag to generate the help message produced by PrintUsage. We will
// return an error if we find a field that is not documented. You should
// use `doc:"-"` to force the parser to skip a field.
//
// The name of the structure field is converted to kebab case and
// used to generate the command line option.
//
// To define a short option, add the `short:"v"` tag, where "v" must be
// a string containing a single one-byte character. If present, we'll use
// the value in short to determine the short option name.
//
// The `required:"true"` tag indicates that an option is required.
//
// For example:
//
//     type CLI struct {
//       Help    bool   `doc:"prints this help message" short:"h"`
//       Input   string `doc:"sets the URL to measure"  required:"true"`
//       Verbose bool   `doc:"runs in verbose mode"     short:"v"`
//     }
//
// becomes:
//
//     program [-h,--help] --input value [-v,--verbose]
//
// Note that, by default, this parser does not treat `-h` or `--help`
// specially; you'll need to implement actions for them.
//
// By default, the returned parser accepts any number of positional arguments, as
// the original getopt does. You can change that by using, e.g., the
// NoPositionalArguments Config constructor to force the parser to fail
// if the user has specified one or more positional arguments.
//
// For example:
//
//   parser, err := getoptx.NewParser(&cli, getoptx.NoPositionalArguments())
//
// Likewise, you can use SetProgramName and SetPositionalArguments
// to control exactly how PrintUsage works.
//
// This constructor returns either a valid parser and a nil error, on
// success, or a nil parser and an error, on failure.
func NewParser(flags interface{}, configs ...Config) (Parser, error) {
	return newParserWrapper(flags, configs...)
}

func newParserWrapper(flags interface{}, configs ...Config) (*parserWrapper, error) {
	parser := getopt.New()

	// 1. flags must be a pointer to structure. We obtain
	// the structure type and its value.
	value := reflect.ValueOf(flags)
	if value.Kind() != reflect.Ptr {
		return nil, errors.New("expected a pointer")
	}
	pointee := value.Elem()
	if pointee.Kind() != reflect.Struct {
		return nil, errors.New("expected a pointer to struct")
	}
	pointeeType := pointee.Type()

	// 2. we process each field inside the struct.
	docs := make(map[string]string)
	required := make(map[string]bool)
	for idx := 0; idx < pointeeType.NumField(); idx++ {

		// 3. obtain the field value, a pointer to the value, the
		// field type, and the associated tags.
		fieldValue := pointee.Field(idx)
		if !fieldValue.CanAddr() {
			return nil, errors.New("cannot obtain the address of a field")
		}
		fieldValuePtr := fieldValue.Addr()
		fieldType := pointeeType.Field(idx)
		tag := fieldType.Tag

		// 4. every field must contain documentation. However, we skip
		// fields named "-" like encoding/json also does.
		docstring := tag.Get("doc")
		if docstring == "-" {
			continue
		}
		if docstring == "" {
			return nil, errors.New("there is a field without documentation")
		}

		// 5. a field may have a short associated option.
		short := rune(0)
		if shortName := tag.Get("short"); shortName != "" {
			if len(shortName) != 1 {
				return nil, errors.New("the short tag's value must contain a single-byte string")
			}
			short, _ = utf8.DecodeRune([]byte(shortName))
		}

		// 6. the long option name is the kebab-case of the field name.
		name := strcase.ToKebab(fieldType.Name)
		docs[name] = docstring

		// 7. add this option to pborman's parser.
		if !fieldValuePtr.CanInterface() {
			return nil, errors.New("a field inside the structure is private")
		}
		opt := parser.FlagLong(fieldValuePtr.Interface(), name, short, docstring)
		switch fieldValuePtr.Interface().(type) {
		case *Counter:
			opt.SetFlag()
		default:
			// nothing
		}

		// 8. an option could be marked as required.
		if tag.Get("required") == "true" {
			required[name] = true
			opt.Mandatory()
		}
	}

	// 9. wrap pborman's parser.
	pw := &parserWrapper{
		set:      parser,
		docs:     docs,
		pac:      newPositionalArgumentsChecker(),
		required: required,
	}

	// 10. apply config bits
	for _, config := range configs {
		config.visit(pw)
	}
	return pw, nil
}

// parserWrapper wraps a getopt.Set to implement extra functionality.
type parserWrapper struct {
	// Set is the underlying cmdline parser.
	set *getopt.Set

	// docs contains the documentation.
	docs map[string]string

	// pac is the positional arguments checker.
	pac *positionalArgumentsChecker

	// required tracks the required options.
	required map[string]bool
}

// numOptions counts the number of registered options.
func (p *parserWrapper) numOptions() int {
	return len(p.docs)
}

// maybeAddHelpFlags attempts to register -h/--help. If the user
// has already configured -h/--help we'll just do nothing.
func (p *parserWrapper) maybeAddHelpFlags(help *bool) bool {
	var found bool
	p.set.VisitAll(func(o getopt.Option) {
		found = found || o.ShortName() == "h" || o.LongName() == "help"
	})
	if found {
		return false
	}
	p.set.FlagLong(help, "help", 'h', "Prints this help message")
	p.docs["help"] = "Prints this help message"
	return true
}

// Args implements Parser.Args.
func (p *parserWrapper) Args() []string {
	return p.set.Args()
}

// NArgs implements Parser.NArgs.
func (p *parserWrapper) NArgs() int {
	return p.set.NArgs()
}

// positionalArgumentsChecker checks whether the number
// of positional arguments is acceptable.
type positionalArgumentsChecker struct {
	// minArgs is the minimum acceptable number of positional arguments.
	minArgs int

	// maxArgs is the maximum acceptable number of positional arguments.
	maxArgs int
}

// newPositionalArgumentsChecker creates a new checker for positional arguments.
func newPositionalArgumentsChecker() *positionalArgumentsChecker {
	return &positionalArgumentsChecker{
		minArgs: -1,          // allow any number of positional arguments
		maxArgs: math.MaxInt, // ditto
	}
}

var (
	// ErrTooManyPositionalArguments indicates that you passed too many
	// positional arguments to the current parser.
	ErrTooManyPositionalArguments = errors.New("too many positional arguments")

	// ErrTooFewPositionalArguments indicates that you passed too few
	// positional arguments to the current parser.
	ErrTooFewPositionalArguments = errors.New("too few positional arguments")
)

// Getopt implements Parser.Getopt.
func (p *parserWrapper) Getopt(args []string) error {
	if err := p.set.Getopt(args, nil); err != nil {
		return err
	}
	return p.pac.check(p)
}

func (pac *positionalArgumentsChecker) check(p Parser) error {
	count := p.NArgs()
	if count < pac.minArgs {
		return ErrTooFewPositionalArguments
	}
	if count > pac.maxArgs {
		return ErrTooManyPositionalArguments
	}
	return nil
}

// MustGetopt implements Parser.MustGetopt.
func (p *parserWrapper) MustGetopt(args []string) {
	if err := p.Getopt(args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		p.PrintUsage(os.Stderr)
		os.Exit(1)
	}
}

// PrintUsage implements Parser.PrintUsage.
func (p *parserWrapper) PrintUsage(w io.Writer) {
	p.printBriefUsage(w)
	fmt.Fprintf(w, "\n")
	p.printOptions(w)
}

func (p *parserWrapper) printOptions(w io.Writer) {
	fmt.Fprintf(w, "Options:\n\n")
	p.set.VisitAll(func(o getopt.Option) {
		if o.ShortName() != "" {
			fmt.Fprintf(w, "  -%s, --%s", o.ShortName(), o.LongName())
		} else {
			fmt.Fprintf(w, "      --%s", o.LongName())
		}
		if !o.IsFlag() {
			fmt.Fprintf(w, " value")
		}
		fmt.Fprintf(w, "\n")
		doc := p.docs[o.LongName()]
		if !strings.HasSuffix(doc, ".") {
			doc += "."
		}
		if p.required[o.LongName()] {
			doc += " This option is mandatory."
		}
		for _, line := range strings.Split(wordwrap.WrapString(doc, 64), "\n") {
			fmt.Fprintf(w, "             %s\n", line)
		}
		fmt.Fprintf(w, "\n")
	})
}

func (p *parserWrapper) printBriefUsage(w io.Writer) {
	var parameters string
	if p.pac.maxArgs >= 1 {
		parameters = p.set.Parameters()
	}
	fmt.Fprintf(w, "\nUsage: %s [options] %s\n", p.set.Program(), parameters)
}

// SetProgramName sets the program name printed in the usage string.
//
// If the provided name is empty, this option does not modify the
// program name that would be printed by default.
func SetProgramName(name string) Config {
	return &setProgramName{name: name}
}

type setProgramName struct {
	name string
}

func (c *setProgramName) visit(p *parserWrapper) {
	if c.name != "" {
		p.set.SetProgram(c.name)
	}
}

// SetPositionalArgumentsPlaceholder allows a user to set the name given to
// the positional arguments in the usage string.
//
// Note that this value will not be printed if the parser does not accept
// at least a single positional argument.
//
// If the provided name is empty, we will not modify the name that would
// printed by default for representing positional arguments.
func SetPositionalArgumentsPlaceholder(name string) Config {
	return &setPositionalArgumentsPlaceholder{name: name}
}

type setPositionalArgumentsPlaceholder struct {
	name string
}

func (c *setPositionalArgumentsPlaceholder) visit(p *parserWrapper) {
	if c.name != "" {
		p.set.SetParameters(c.name)
	}
}

type minMaxPositionalArguments struct {
	minArgs int
	maxArgs int
}

func (par *minMaxPositionalArguments) visit(p *parserWrapper) {
	p.pac.minArgs = par.minArgs
	p.pac.maxArgs = par.maxArgs
}

// NoPositionalArguments is a bit of config that causes Parse to
// fail if the user has provided any positional argument.
func NoPositionalArguments() Config {
	return &minMaxPositionalArguments{
		minArgs: 0,
		maxArgs: 0,
	}
}

// AtLeastOnePositionalArgument is a bit of config that causes Parse
// to fail if the user has provided no positional arguments.
func AtLeastOnePositionalArgument() Config {
	return &minMaxPositionalArguments{
		minArgs: 1,
		maxArgs: math.MaxInt,
	}
}

// JustOnePositionalArgument is a bit of config that causes Parse to fail
// if the user has not provided exactly one positional argument.
func JustOnePositionalArgument() Config {
	return &minMaxPositionalArguments{
		minArgs: 1,
		maxArgs: 1,
	}
}
