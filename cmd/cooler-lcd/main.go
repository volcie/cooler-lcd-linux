// Command cooler-lcd drives the LCD panel on CPU coolers built around the HID
// 5131:2007 controller.
package main

import (
	"os"

	"github.com/volcie/cooler-lcd-linux/internal/app"
)

func main() {
	os.Exit(app.Execute())
}
