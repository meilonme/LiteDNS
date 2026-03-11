package main

import (
	"log"

	"litedns/internal/app"
)

func main() {
	a, err := app.New()
	if err != nil {
		log.Fatalf("bootstrap failed: %v", err)
	}

	if err := a.Run(); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}
