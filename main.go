package main

import (
	"log"
	"os"
	"school-erp/config"
	_ "school-erp/docs"
	"school-erp/middleware"
	"school-erp/routes"

	"github.com/gofiber/fiber/v2"
)

// @title School ERP API
// @version 1.0
// @description REST API for School ERP
// @BasePath /api
func main() {
	config.ConnectDB()
	config.RunMigrations()

	app := fiber.New()
	middleware.Setup(app)
	routes.Setup(app)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	log.Fatal(app.Listen(":" + port))
}
