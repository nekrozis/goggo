# goggo

A command-line client for working with GOG games, written in Go.

goggo provides account browsing, offline downloads, local installation and
verification, orphan file management, and checksum tools.

## Quick start

Log in:

    goggo auth login

List owned games:

    goggo list games

Inspect a game:

    goggo game terraria

Install or update a game:

    goggo install terraria

Check an installed game:

    goggo verify terraria

Run `goggo <command> --help` for command-specific options.

## Common tasks

List available offline files:

    goggo backup list terraria

Download offline files:

    goggo backup download terraria

Find unrecognized files in an installation:

    goggo orphans check terraria

Create a checksum file:

    goggo manifest create <file>

Inspect a checksum file:

    goggo manifest inspect <file>

Check a file against its checksum file:

    goggo manifest verify <file>

Inspect Galaxy builds, manifests, or CDN endpoints:

    goggo galaxy builds terraria
    goggo galaxy manifest terraria
    goggo galaxy cdns terraria

## Authentication

goggo auth login starts the GOG authentication flow.

Authentication data is stored locally in the user's configuration
directory and is not uploaded by goggo.

Check the current authentication state with:

    goggo auth status

Clear locally stored authentication data with:

    goggo auth clear

## License

BSD-3-Clause. See LICENSE for details.

goggo is an independent project and is not affiliated with or endorsed
by GOG Ltd.

Game content and trademarks belong to their respective owners.

## Acknowledgments

goggo is inspired by lgogdownloader.

Thanks to Sławomir Nizio and the lgogdownloader contributors for their
work on the original project.
