package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
)

// renderVersion prints the program's identity, then the upstream release this
// port tracks: the compatibility baseline is metadata, never presented as our
// version.
func renderVersion(w io.Writer) {
	fmt.Fprintln(w, config.VersionString)
	fmt.Fprintf(w, "%s compatibility: %s\n", config.UpstreamName, config.UpstreamCompatibilityVersion)
}

// usage writes the CLI's surface for a topic path (empty for the whole CLI).
//
// The text is generated from the parser's own tables — the command tree and the
// option table — so the help cannot drift from what the parser accepts.
//
// Interim (CLI1 S2): one level, shared options only, no long descriptions. S3
// owns the layered help: per-command topics, the option groups, the hidden set,
// and the decision on how an unknown topic is answered (registered in the S1
// audit).
func usage(w io.Writer, path []string) {
	if len(path) > 0 {
		if text, ok := commandUsage(path); ok {
			fmt.Fprint(w, text)
			return
		}
	}
	fmt.Fprint(w, generalUsage())
}

// generalUsage lists the commands and the options every command shares.
func generalUsage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s <command> [options]\n\nCommands:\n", config.ProgramName)
	for _, node := range commandTree {
		b.WriteString(commandLines(node, "  "))
	}
	b.WriteString(commandLines(commandNode{name: "help", summary: "Show help for a command"}, "  "))
	b.WriteString(commandLines(commandNode{name: "version", summary: "Show version"}, "  "))
	b.WriteString("\nShared options:\n")
	b.WriteString(optionLines(sharedOptions, "  "))
	return b.String()
}

// commandUsage renders one command's topic: what it does and what it accepts.
func commandUsage(path []string) (string, bool) {
	node, _, rest, err := resolveCommand(path)
	if err != nil || node.name == "" || len(rest) != 0 {
		return "", false
	}
	var b strings.Builder
	if len(node.children) != 0 {
		fmt.Fprintf(&b, "Usage: %s %s <subcommand> [options]\n\nSubcommands:\n", config.ProgramName, strings.Join(path, " "))
		for _, child := range node.children {
			fmt.Fprintf(&b, "  %-16s %s\n", child.name, child.summary)
		}
		return b.String(), true
	}
	fmt.Fprintf(&b, "Usage: %s %s", config.ProgramName, strings.Join(path, " "))
	if want, takes := commandArity(node.id); takes {
		fmt.Fprintf(&b, " <%s>", want)
	}
	fmt.Fprintf(&b, " [options]\n\n%s\n", node.summary)
	if len(node.options) != 0 {
		b.WriteString("\nOptions:\n")
		b.WriteString(optionLines(optionSet(node.options), "  "))
	}
	return b.String(), true
}

// commandLines renders one node of the tree, indenting its children.
func commandLines(node commandNode, indent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s%s\n", len(indent)+24, indent+node.name, node.summary)
	for _, child := range node.children {
		fmt.Fprintf(&b, "%-*s%s\n", len(indent)+2+24, indent+"  "+child.name, child.summary)
	}
	return b.String()
}

// optionLines renders the options in ids, in table order, hiding the ones the
// table marks hidden.
func optionLines(ids []optionID, indent string) string {
	var b strings.Builder
	for i := range optionTable {
		spec := &optionTable[i]
		if spec.hidden || !containsOption(ids, spec.id) {
			continue
		}
		name := "--" + spec.long
		if spec.arg != "" {
			name += " " + spec.arg
		}
		short := ""
		if len(spec.aliases) != 0 {
			short = " (-" + spec.aliases[0] + ")"
		}
		fmt.Fprintf(&b, "%-*s%s\n", len(indent)+28, indent+name+short, spec.summary)
	}
	return b.String()
}
