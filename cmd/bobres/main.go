// Command bobres is the BOBRES operator CLI (install, update, doctor, ...).
package main

import (
	"fmt"
	"os"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("bobres", version.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "bobres: command not implemented yet")
	os.Exit(2)
}
