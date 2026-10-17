//go:build !windows

package main

import (
	"fmt"
	"os"
)

func attachConsole() {}

func showError(msg string) { fmt.Fprintln(os.Stderr, "Fehler:", msg) }

// defaultCommand is what runs without arguments: the server in the foreground.
func defaultCommand() string { return "run" }
