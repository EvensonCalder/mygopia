package main

import (
	"log"

	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	_ "github.com/egoist/mygo/locales/zh-Hant"

	"mygopia/internal/app"
)

func main() {
	if err := app.RunDesktop(); err != nil {
		log.Fatal(err)
	}
}
