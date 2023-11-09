package tools

import (
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/sirupsen/logrus"
	"gorm.io/gen"
	"nft.house/service/db_models"
)

// Dynamic SQL
type Querier interface {
	// SELECT * FROM @@table WHERE name = @name{{if role !=""}} AND role = @role{{end}}
	FilterWithNameAndRole(name, role string) ([]gen.T, error)
}

func GormGen() {
	g := gen.NewGenerator(gen.Config{
		OutPath: "./service/query",
		Mode:    gen.WithoutContext | gen.WithDefaultQuery | gen.WithQueryInterface, // generate mode
	})

	config := mysql.MustNewConfigFromViper()
	db := config.MustOpenOrCreate()
	g.UseDB(db) // reuse your gorm db

	// Generate basic type-safe DAO API for struct `model.User` following conventions
	g.ApplyBasic(db_models.MigrationModels...)

	// Generate Type Safe API with Dynamic SQL defined on Querier interface for `model.User` and `model.Company`
	//g.ApplyInterface(func(Querier){}, model.User{}, model.Company{})

	// Generate the code
	g.Execute()

	logrus.Info("done")
}
