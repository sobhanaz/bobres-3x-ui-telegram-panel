// Command core is the BOBRES core service.
package main

import (
	"os"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
)

func main() {
	os.Exit(app.Run("core", os.Args[1:], nil))
}
