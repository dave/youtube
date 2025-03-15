package main

import (
	"context"
	"log"

	"github.com/dave/youtube2/upload"
)

func main() {
	service := upload.New("UCFDggPICIlCHp3iOWMYt8cg")
	if err := service.Start(context.Background()); err != nil {
		log.Fatalf("Unable to initialise service: %v", err)
	}
}
