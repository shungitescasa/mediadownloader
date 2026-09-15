package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"tools/mediadownloader/internal/dl"

	"github.com/gofiber/fiber/v3"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dl.StartWorker(ctx)

	app := fiber.New()

	app.Post("/queue", dl.AddToQueue)

	log.Fatalln(app.Listen(":3056"))
}
