// @scope.file cmd.main
// @hash sha256:b45a342bb064c5f6
// @implements spec.architecture sha256:de075e4493c51e2f
// @ref default app.core sha256:55bd88b4ebb6da29
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
