package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"school-erp/config"
	"school-erp/seed"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/cli/main.go <command> [options]")
		fmt.Println("Commands:")
		fmt.Println("  seed                Run idempotent platform & tenant catalog seeder and refresh Redis caches")
		fmt.Println("  create-root-admin   Create or ensure a Platform ROOT_ADMIN user")
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "seed":
		config.ConnectDB()
		config.RunMigrations()
		config.ConnectRedis()
		if err := seed.Run(config.DB); err != nil {
			log.Fatalf("❌ Lỗi chạy seeder: %v", err)
		}
		seed.InvalidateCaches(config.RedisClient)
		log.Println("✅ Hoàn tất seed dữ liệu nền tảng & Tenant Catalog")
	case "create-root-admin":
		fs := flag.NewFlagSet("create-root-admin", flag.ExitOnError)
		email := fs.String("email", "admin@school-erp.vn", "Root admin email")
		password := fs.String("password", "Admin@123456", "Root admin password")
		name := fs.String("name", "Super Admin", "Root admin full name")
		_ = fs.Parse(os.Args[2:])

		config.ConnectDB()
		config.RunMigrations()

		if err := seed.EnsureRootAdmin(config.DB, *email, *password, *name); err != nil {
			log.Fatalf("❌ Lỗi tạo tài khoản ROOT_ADMIN: %v", err)
		}
		log.Printf("✅ Hoàn tất tạo/gán quyền ROOT_ADMIN cho %s\n", *email)
	default:
		log.Fatalf("❌ Lệnh không hợp lệ: %s", cmd)
	}
}
