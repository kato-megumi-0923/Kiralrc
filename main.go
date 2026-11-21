package main

import (
	"log"

	"kiralrc-gui/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
