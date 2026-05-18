package main

import (
	"log"
	dl "tools/mediadownloader"

	"github.com/gofiber/fiber/v3"
)

func main() {
	app := fiber.New()

	app.Get("/download", dl.StartDownload)

	log.Fatalln(app.Listen(":3056"))
}
