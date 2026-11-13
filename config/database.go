package config

// Package config provides helpers for connecting to PostgreSQL via GORM.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

// LoadDotEnv searches for a .env file in parent directories and loads it.
func LoadDotEnv() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			return godotenv.Load(envPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return fmt.Errorf(".env not found in any parent directory")
}

// ConnectDB initializes the global PostgreSQL connection using environment
// variables. It should be called once at application startup.
func ConnectDB() {
	start := time.Now()

	if err := LoadDotEnv(); err != nil {
		log.Println("⚠️ Không tìm thấy .env, sẽ dùng biến môi trường hệ thống nếu có")
	} else {
		log.Println("✅ Đã load file .env thành công")
	}

	host := os.Getenv("DB_HOST")
	if host == "" {
		log.Fatal("❌ DB_HOST không được để trống")
	}

	port := os.Getenv("DB_PORT")
	if port == "" {
		log.Fatal("❌ DB_PORT không được để trống")
	}

	user := os.Getenv("DB_USER")
	if user == "" {
		log.Fatal("❌ DB_USER không được để trống")
	}

	password := os.Getenv("DB_PASSWORD")
	if password == "" {
		log.Fatal("❌ DB_PASSWORD không được để trống")
	}

	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		log.Fatal("❌ DB_NAME không được để trống")
	}

	sslMode := os.Getenv("DB_SSLMODE")
	if sslMode == "" {
		sslMode = "disable"
	}

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		host, user, password, dbName, port, sslMode,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("❌ Lỗi kết nối PostgreSQL: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("❌ Lỗi lấy sql.DB từ GORM: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		log.Fatalf("❌ Không thể ping PostgreSQL: %v", err)
	}

	DB = db
	log.Printf("✅ Kết nối PostgreSQL thành công sau %v\n", time.Since(start))
}

// RunMigrations executes pending SQL migrations from ./migrations using golang-migrate.
func RunMigrations() {
	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatalf("❌ Lỗi lấy sql.DB để migrate: %v", err)
	}

	driver, err := migratepg.WithInstance(sqlDB, &migratepg.Config{})
	if err != nil {
		log.Fatalf("❌ Lỗi khởi tạo driver golang-migrate: %v", err)
	}

	m, err := migrate.NewWithDatabaseInstance("file://migrations", "postgres", driver)
	if err != nil {
		log.Fatalf("❌ Lỗi đọc thư mục migrations: %v", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("❌ Lỗi chạy migration: %v", err)
	}

	log.Println("✅ Chạy database migrations thành công")
}
