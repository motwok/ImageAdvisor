// SPDX-License-Identifier: AGPL-3.0-or-later

// Command imageadvisor reports which running containers have a newer image in their registry.
package main

import (
	"os"

	"github.com/motwok/ImageAdvisor/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdout, os.Stderr))
}
