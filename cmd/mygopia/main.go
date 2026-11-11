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
