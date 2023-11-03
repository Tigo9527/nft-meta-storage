package web

import (
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"nft.house/service"
)

func Start() {
	service.MustInit()

	endpoint := viper.GetString("api.endpoint")
	logrus.WithFields(logrus.Fields{"endpoint": endpoint}).Info("run web server")
	api.MustServeFromViper(Routes)
}
