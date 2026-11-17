package config

import (
	"context"
	"log"
	"os"
	"school-erp/internal/pkg/storage"
	"strings"
	"time"
)

var StorageClient storage.ObjectStorage

// InitStorage initializes the global S3 / Cloudflare R2 client from environment variables.
func InitStorage() {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	usePathStyle := true
	if p := strings.ToLower(strings.TrimSpace(os.Getenv("S3_USE_PATH_STYLE"))); p == "false" || p == "0" {
		usePathStyle = false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := storage.NewS3Client(ctx, storage.Config{
		Provider:      os.Getenv("S3_PROVIDER"),
		Endpoint:      endpoint,
		Region:        os.Getenv("S3_REGION"),
		Bucket:        os.Getenv("S3_BUCKET"),
		AccessKeyID:   os.Getenv("S3_ACCESS_KEY_ID"),
		SecretKey:     os.Getenv("S3_SECRET_ACCESS_KEY"),
		PublicBaseURL: os.Getenv("S3_PUBLIC_BASE_URL"),
		UsePathStyle:  usePathStyle,
	})
	if err != nil {
		log.Printf("⚠️ Lỗi khởi tạo S3/R2 Storage Client: %v\n", err)
		return
	}

	StorageClient = client
	if client.IsConfigured() {
		log.Printf("✅ Đã khởi tạo %s Storage Client (Bucket: %s, Endpoint: %s)\n", client.Provider(), client.Bucket(), endpoint)
	} else {
		log.Println("⚠️ Chưa cấu hình S3_BUCKET / S3_ACCESS_KEY_ID / S3_SECRET_ACCESS_KEY (tính năng upload ảnh tạm chờ cấu hình)")
	}
}
