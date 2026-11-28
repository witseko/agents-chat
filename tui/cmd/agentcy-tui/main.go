// Command agentcy-tui is the human's terminal UI for agentcy. It talks to the
// running core over the operator protocol on the core's Unix socket. Start it with
// `agentcy ui`, which makes sure the core runs and then starts this program.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

// version is the release version. The Makefile sets it at build time with -ldflags.
var version = "dev"

// main parses the flags, then runs the UI until the user quits.
func main() {
	workspace := flag.String("workspace", ".", "checkout directory whose conversations to manage")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("agentcy-tui", version)
		return
	}
	if err := run(*workspace); err != nil {
		fmt.Fprintln(os.Stderr, "agentcy-tui:", err)
		os.Exit(1)
	}
}

// run resolves the checkout and the bridge data directory and runs the program.
func run(workspace string) error {
	directory, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace %s is not a directory", directory)
	}
	home, err := bridgeHome()
	if err != nil {
		return err
	}
	ui := newModel(NewClient(home), directory)
	ui.dataDir = home
	_, err = tea.NewProgram(ui).Run()
	return err
}
