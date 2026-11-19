package main

import (
	"context"
	"log"
	"os"
	"school-erp/config"
	_ "school-erp/docs"
	platformmedia "school-erp/internal/platform/media"
	"school-erp/middleware"
	"school-erp/routes"
	"school-erp/seed"

	"github.com/gofiber/fiber/v2"
)

// @title School ERP API
// @version 1.0
// @description REST API for School ERP
// @BasePath /api
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Nhập theo cú pháp: Bearer <token>
func main() {
	config.ConnectDB()
	config.RunMigrations()
	config.ConnectRedis()
	config.InitCloudflare()
	config.InitStorage()

	if err := seed.Run(config.DB); err != nil {
		log.Printf("⚠️ Lỗi chạy seed dữ liệu: %v\n", err)
	}

	platformmedia.NewService(config.DB, config.StorageClient, nil, nil).StartTrashCleanupWorker(context.Background())

	app := fiber.New(fiber.Config{
		BodyLimit: 28 * 1024 * 1024, // 28 MB (hỗ trợ tệp ảnh/tài liệu tối đa 25 MB + overhead multipart)
	})
	middleware.Setup(app)
	routes.Setup(app)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	log.Fatal(app.Listen(":" + port))
}
