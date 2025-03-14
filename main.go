package main

import (
	"context"
	"log"
)

const YOUTUBE_CHANNEL_ID = "UCFDggPICIlCHp3iOWMYt8cg"

func main() {
	ctx := context.Background()

	service := &Service{}

	if err := service.Init(ctx); err != nil {
		log.Fatalf("Unable to initialise service: %v", err)
	}

}
