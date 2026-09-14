package main

import (
	"log"
	"tools/mediadownloader/internal/dl"

	"github.com/gofiber/fiber/v3"
)

func main() {
	app := fiber.New()

	app.Post("/queue", dl.AddToQueue)

	log.Fatalln(app.Listen(":3056"))
}
