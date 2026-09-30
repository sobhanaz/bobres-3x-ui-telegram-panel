// Command payments is the BOBRES payments service.
package main

import (
	"os"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
)

func main() {
	os.Exit(app.Run("payments", os.Args[1:], nil))
}
