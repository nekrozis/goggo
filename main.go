// Command goggo is a GOG downloader CLI for account game access and local
// installation management.
//
// The front end lives in internal/cli; this file only provides the process
// entry point and forwards command-line arguments and standard IO.
package main

import (
	"os"

	"github.com/nekrozis/goggo/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
