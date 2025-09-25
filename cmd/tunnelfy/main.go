package main

import (
	"log"
	"os"

	"tunnelfy/internal/app"
)

func main() {
	application, err := app.New()
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	if err := application.Start(); err != nil {
		log.Fatalf("Application error: %v", err)
		os.Exit(1)
	}
}
