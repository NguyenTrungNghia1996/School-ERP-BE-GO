package repositories

import (
	"school-erp/config"

	"gorm.io/gorm"
)

func DB() *gorm.DB {
	return config.DB
}

