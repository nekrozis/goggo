# goggo

A command-line GOG downloader and installation manager written in Go.

goggo downloads offline game files from a GOG account and manages local
installations using GOG metadata and manifests.

## About

goggo is a standalone Go implementation for working with GOG game
downloads and local installations.

It supports account queries, offline backups, manifest-based installation
and verification, and inspection of GOG metadata from the website and
Galaxy APIs.

## Installation

Download the archive for your platform from the releases page and place
the `goggo` binary somewhere in your `PATH`.

### Build from source

Requirements:

- Go 1.27 or later

Build:

    go build .

## Quick start

Log in:

    goggo auth login

List owned games:

    goggo list games

Inspect a game:

    goggo game terraria

Install or update a local installation:

    goggo install terraria

Use `goggo <command> --help` for command-specific options.

## Authentication

goggo stores authentication data in the user's configuration directory.

Authentication data is kept locally and is not uploaded by goggo.

Check authentication state:

    goggo auth status

Remove stored authentication data:

    goggo auth clear

`auth login` starts the GOG authentication flow.

## Commands

| Command | Description |
|---------|-------------|
| auth | Manage authentication state |
| list | List owned games, tags, and wishlist entries |
| game | Show product information |
| galaxy | Inspect Galaxy builds, manifests, and CDN information |
| backup | List and download offline backup files |
| install | Install or update files according to a manifest |
| verify | Verify installed files against a manifest |
| orphans | Find or remove files not referenced by manifests |
| manifest | Inspect and manage XML checksum manifests |

## Development

Build:

    go build ./...

Test:

    go test ./...

## License

BSD-3-Clause. See LICENSE for details.

Dependencies:

- Go standard library
- golang.org/x/net (BSD-3-Clause)
- golang.org/x/sys (BSD-3-Clause)
- golang.org/x/term (BSD-3-Clause)

goggo is an independent project and is not affiliated with or endorsed
by GOG Ltd.

Game content and trademarks belong to their respective owners.

## Acknowledgments

goggo is inspired by lgogdownloader.

Thanks to Sławomir Nizio and the lgogdownloader contributors for their
work on the original project.
