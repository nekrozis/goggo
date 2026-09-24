package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
)

// renderVersion prints the program's identity, then the compatibility baseline
// of the release it tracks: that is metadata, never presented as our version.
func renderVersion(w io.Writer) {
	fmt.Fprintln(w, config.VersionString)
	fmt.Fprintf(w, "%s compatibility: %s\n", config.UpstreamName, config.UpstreamCompatibilityVersion)
}

// usage writes the help for a topic path (empty for the CLI itself).
//
// Every layer is generated from the parser's own tables — the command tree and
// the option table — so the help cannot describe a command the parser does not
// have or an option it does not accept. The topic path arrives already resolved
// and validated (parseArgs owns that rule), so this layer only renders.
func usage(w io.Writer, path []string) {
	node, ok := resolveTopic(path)
	if !ok {
		fmt.Fprint(w, rootUsage())
		return
	}
	switch {
	case len(path) == 0:
		fmt.Fprint(w, rootUsage())
	case len(node.children) != 0 && node.id == cmdNone:
		fmt.Fprint(w, namespaceUsage(node, path))
	default:
		fmt.Fprint(w, commandUsage(node, path))
	}
}

// rootUsage lists the CLI's commands and the options they all share.
func rootUsage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s <command> [options]\n\nCommands:\n", config.ProgramName)
	for _, node := range commandTree {
		b.WriteString(commandLines(node))
	}
	b.WriteString(commandLines(commandNode{name: "help", summary: "Show help for a command or the CLI"}))
	b.WriteString(commandLines(commandNode{name: "version", summary: "Show version"}))
	b.WriteString("\nShared options:\n")
	b.WriteString(optionLines(sharedOptions))
	fmt.Fprintf(&b, "\nRun '%s <command> -h' for the options of one command.\n", config.ProgramName)
	return b.String()
}

// namespaceUsage lists a namespace's subcommands.
func namespaceUsage(node commandNode, path []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s %s <subcommand> [options]\n\n%s\n\nSubcommands:\n",
		config.ProgramName, strings.Join(path, " "), node.summary)
	for _, child := range node.children {
		fmt.Fprintf(&b, "  %-16s %s\n", child.name, child.summary)
	}
	return b.String()
}

// commandUsage renders one command: how to call it, what it does, what the user
// should know before running it, and what it accepts.
func commandUsage(node commandNode, path []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s %s", config.ProgramName, strings.Join(path, " "))
	if want, count := commandArity(node.id); count != 0 {
		switch {
		case count == -2:
			// Zero-or-more reads as an optional list in the usage line.
			fmt.Fprintf(&b, " [%s]...", want)
		case node.id == cmdBackupDownload:
			fmt.Fprintf(&b, " <%s> [<file>...]", want)
		case count < 0:
			fmt.Fprintf(&b, " <%s>...", want)
		case count == 2:
			fmt.Fprintf(&b, " <%s> [build]", want)
		default:
			fmt.Fprintf(&b, " <%s>", want)
		}
	}
	fmt.Fprintf(&b, " [options]\n\n%s\n", node.summary)

	if len(node.notes) != 0 {
		b.WriteString("\n")
		for _, note := range node.notes {
			fmt.Fprintf(&b, "%s\n", note)
		}
	}

	// The shared options are listed first when the command adds its own, so a
	// reader sees the command's own knobs without having to subtract.
	if len(node.options) != 0 {
		b.WriteString("\nOptions:\n")
		b.WriteString(detailedOptionLines(optionSet(node.options)))
		b.WriteString("\nAlso accepted (shared with every command):\n")
		b.WriteString(optionLines(sharedOptions))
	} else {
		b.WriteString("\nOptions:\n")
		b.WriteString(detailedOptionLines(sharedOptions))
	}

	// A node that is both leaf and namespace ("download") shows its
	// subcommands under its own usage, so the topic says everything the word
	// can mean.
	if len(node.children) != 0 {
		b.WriteString("\nSubcommands:\n")
		for _, child := range node.children {
			fmt.Fprintf(&b, "  %-16s %s\n", child.name, child.summary)
		}
	}
	return b.String()
}

// resolveTopic walks a path against the tree, meta commands included.
func resolveTopic(path []string) (commandNode, bool) {
	if len(path) == 0 {
		return commandNode{}, true // the root topic
	}
	nodes := append(append([]commandNode{}, commandTree...), metaCommands...)
	var node commandNode
	for _, name := range path {
		child, ok := findChild(nodes, name)
		if !ok {
			return commandNode{}, false
		}
		node = child
		nodes = child.children
	}
	return node, true
}

// commandLines renders one node of the tree, indenting its children.
func commandLines(node commandNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %-24s%s\n", node.name, node.summary)
	for _, child := range node.children {
		fmt.Fprintf(&b, "    %-22s%s\n", child.name, child.summary)
	}
	return b.String()
}

// optionLines renders the options in ids, in table order, hiding the ones the
// table marks hidden.
func optionLines(ids []optionID) string {
	var b strings.Builder
	for i := range optionTable {
		spec := &optionTable[i]
		if spec.hidden || !containsOption(ids, spec.id) {
			continue
		}
		fmt.Fprintf(&b, "  %-26s%s\n", optionUsage(spec), spec.summary)
	}
	return b.String()
}

// detailedOptionLines renders the same list with each option's longer
// description below it, for the topic of a single command.
func detailedOptionLines(ids []optionID) string {
	var b strings.Builder
	for i := range optionTable {
		spec := &optionTable[i]
		if spec.hidden || !containsOption(ids, spec.id) {
			continue
		}
		fmt.Fprintf(&b, "  %-26s%s\n", optionUsage(spec), spec.summary)
		for line := range strings.SplitSeq(spec.detail, "\n") {
			if line == "" {
				continue
			}
			fmt.Fprintf(&b, "  %-26s%s\n", "", line)
		}
	}
	return b.String()
}

// optionUsage is the option as a user types it, with its short alias and its
// value placeholder.
func optionUsage(spec *optionSpec) string {
	name := "--" + spec.long
	if spec.arg != "" {
		name += " " + spec.arg
	}
	if len(spec.aliases) != 0 {
		name += " (-" + spec.aliases[0] + ")"
	}
	return name
}
