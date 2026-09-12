package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const (
	helloMessage   = "Hello World"
	versionMessage = "sluice dev"
)

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

func run(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sluice", flag.ContinueOnError)
	fs.SetOutput(stdout)

	showVersion := fs.Bool("version", false, "show version")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}

		fmt.Fprintln(stderr, err)
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, versionMessage)
		return 0
	}

	fmt.Fprintln(stdout, helloMessage)
	return 0
}
