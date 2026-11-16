// @scope.file cmd.main
// @hash sha256:b45a342bb064c5f6
// @implements spec.architecture sha256:de075e4493c51e2f
// @ref default app.core sha256:35c4a3caee9a794f
// Desktop executable entrypoint launching the Mygopia UI application loop.

package main

import (
	"log"

	"mygopia/internal/app"
)

func main() {
	if err := app.RunDesktop(); err != nil {
		log.Fatal(err)
	}
}
