// Command bot is the BOBRES bot service.
package main

import (
	"os"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
)

func main() {
	os.Exit(app.Run("bot", os.Args[1:], nil))
}
