package service

import (
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/sirupsen/logrus"
)

var ctx *StorageContext

func MustInit() {
	config := mysql.MustNewConfigFromViper()
	db := config.MustOpenOrCreate()
	err := db.AutoMigrate(MigrationModels...)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to migrate database")
	}

	DB = db

	ctx = SetupContext()
	go CheckPaymentTask()
	go RunUploadWorker()
}
