// Command goggo is a GOG downloader: it logs in to the GOG website, keeps the
// session in a cookie file and a Galaxy token file, and lists or downloads the
// account's games.
//
// The front end lives in internal/cli; this file is only the process entry
// point.
package main

import (
	"os"

	"github.com/nekrozis/goggo/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
