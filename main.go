package main

import (
	"context"
	"log"
)

func main() {
	ctx := context.Background()

	service := &Service{}

	if err := service.Init(ctx); err != nil {
		log.Fatalf("Unable to initialise service: %v", err)
	}

	//for ref, expedition := range service.Expeditions {
	//	fmt.Println(ref)
	//	for _, item := range expedition.Items {
	//		if !item.Video {
	//			continue
	//		}
	//		err := expedition.Templates.ExecuteTemplate(os.Stdout, item.Template, item)
	//		if err != nil {
	//			log.Fatalf("Error executing template: %v", err)
	//		}
	//		fmt.Println()
	//	}
	//}

}
