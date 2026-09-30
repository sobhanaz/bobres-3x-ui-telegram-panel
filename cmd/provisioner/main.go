// Command provisioner is the BOBRES provisioner service.
package main

import (
	"os"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
)

func main() {
	os.Exit(app.Run("provisioner", os.Args[1:], nil))
}
