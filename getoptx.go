// Package getoptx implements a command line parser. Under the hood, we
// use the excellent github.com/pborman/getopt/v2 library.
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
// tag to generate the help message produced by PrintUsage.
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
		// 4. every field must contain documentation.
		docstring := tag.Get("doc")
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
		// 8. an option could be marked as required.
		if tag.Get("required") == "true" {
			required[name] = true
			opt.Mandatory()
		}
	}
	// 9. wrap pborman's parser.
	pw := &parserWrapper{
		Set:      parser,
		docs:     docs,
		minArgs:  -1,          // allow any number of positional arguments
		maxArgs:  math.MaxInt, // ditto
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
	*getopt.Set

	// docs contains the documentation.
	docs map[string]string

	// minArgs is the minimum acceptable number of positional arguments.
	minArgs int

	// maxArgs is the maximum acceptable number of positional arguments.
	maxArgs int

	// required tracks the required options.
	required map[string]bool
}

func (p *parserWrapper) Getopt(args []string) error {
	if err := p.Set.Getopt(args, nil); err != nil {
		return err
	}
	count := len(p.Set.Args())
	if count < p.minArgs {
		return errors.New("too few positional arguments")
	}
	if count > p.maxArgs {
		return errors.New("too many positional arguments")
	}
	return nil
}

func (p *parserWrapper) MustGetopt(args []string) {
	if err := p.Getopt(args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		p.PrintUsage(os.Stderr)
		os.Exit(1)
	}
}

func (p *parserWrapper) PrintUsage(w io.Writer) {
	p.printBriefUsage(w)
	fmt.Fprintf(w, "\nOptions:\n")
	p.Set.VisitAll(func(o getopt.Option) {
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
	if p.maxArgs >= 1 {
		parameters = p.Set.Parameters()
	}
	fmt.Fprintf(w, "\nUsage: %s [options] %s\n", p.Set.Program(), parameters)
}

// SetProgramName sets the program name printed in the usage string.
func SetProgramName(name string) Config {
	return &setProgramName{name: name}
}

type setProgramName struct {
	name string
}

func (c *setProgramName) visit(p *parserWrapper) {
	p.Set.SetProgram(c.name)
}

// SetPositionalArgumentsPlaceholder allows a user to set the name given to
// the positional arguments in the usage string.
//
// Note that this value will not be printed if the parser does not accept
// at least a single positional argument.
func SetPositionalArgumentsPlaceholder(name string) Config {
	return &setPositionalArgumentsPlaceholder{name: name}
}

type setPositionalArgumentsPlaceholder struct {
	name string
}

func (c *setPositionalArgumentsPlaceholder) visit(p *parserWrapper) {
	p.Set.SetParameters(c.name)
}

// NoPositionalArguments is a bit of config that causes Parse to
// fail if the user has provided any positional argument.
func NoPositionalArguments() Config {
	return &noPositionalArguments{}
}

type noPositionalArguments struct{}

func (*noPositionalArguments) visit(p *parserWrapper) {
	p.minArgs = 0
	p.maxArgs = 0
}

// AtLeastOnePositionalArgument is a bit of config that causes Parse
// to fail if the user has provided no positional arguments.
func AtLeastOnePositionalArgument() Config {
	return &atLeastOnePositionalArgument{}
}

type atLeastOnePositionalArgument struct{}

func (*atLeastOnePositionalArgument) visit(p *parserWrapper) {
	p.minArgs = 1
	p.maxArgs = math.MaxInt
}

// JustOnePositionalArgument is a bit of config that causes Parse to fail
// if the user has not provided exactly one positional argument.
func JustOnePositionalArgument() Config {
	return &justOnePositionalArgument{}
}

type justOnePositionalArgument struct{}

func (*justOnePositionalArgument) visit(p *parserWrapper) {
	p.minArgs = 1
	p.maxArgs = 1
}
