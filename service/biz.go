package service

import (
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"nft.house/service/db_models"
	"nft.house/service/query"
)

var ctx *StorageContext

var (
	DB *gorm.DB
)

func MustInit() {
	config := mysql.MustNewConfigFromViper()
	db := config.MustOpenOrCreate()
	err := db.AutoMigrate(db_models.MigrationModels...)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to migrate database")
	}

	DB = db
	query.SetDefault(db)

	ctx = SetupContext()
	go CheckPaymentTask()
	go RunUploadWorker()
	// nft meta migration
	go CheckMigrationTask()
}
