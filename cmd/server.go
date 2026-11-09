package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"tools/mediadownloader/internal/dl"
	"tools/mediadownloader/web"

	"github.com/gofiber/fiber/v3"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dl.Init("mediadownloader.ini")
	dl.StartWorker(ctx)

	app := fiber.New()

	app.Get("/", web.Index)
	app.Use("/static", web.Static())
	app.Post("/queue", dl.AddToQueue)
	app.Get("/download", dl.Download)
	app.Get("/track", dl.Track)
	app.Delete("/stop", dl.Stop)

	log.Fatalln(app.Listen(":3056"))
}
