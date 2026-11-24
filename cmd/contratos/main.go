// Command contratos is the single binary of La Mordida: it imports public
// procurement publications and serves the two rankings over them.
//
// The commands are wired as the cards that own them land; until then the
// binary only answers --help and refuses the rest with a clear message.
package main

import (
	"fmt"
	"os"
)

const usage = `La Mordida — contratación pública, sin ruido.

Usage:
  contratos import [flags]   import publications from a source
  contratos serve [flags]    serve the rankings over the stored data
  contratos version          print the version

The commands are not implemented yet.`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Println(usage)
	case "version", "--version":
		fmt.Println("dev")
	case "import", "serve":
		fmt.Fprintf(os.Stderr, "contratos %s: not implemented yet\n", args[0])
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "contratos: unknown command %q\n\n%s\n", args[0], usage)
		os.Exit(2)
	}
}
