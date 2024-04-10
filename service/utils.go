package service

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"nft.house/service/db_models"
	"strconv"
)

func GetIntConfig(name string, defaultV int) (int, error) {
	var bean db_models.Config
	err := DB.Where("name=?", name).Take(&bean).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultV, nil
	}
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(bean.Value, 10, 64)
	return int(v), err
}

func SaveIntConfig(name string, v int) error {
	strV := fmt.Sprintf("%d", v)
	err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"value": strV}),
	}).Create(&db_models.Config{Name: name, Value: strV}).Error
	if err != nil {
		return errors.WithMessage(err, "SaveIntConfig")
	}
	return nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

type MigrationInfo struct {
	Id             int64  `json:"id"`
	Addr           string `json:"addr"`
	DownloadedMeta int    `json:"downloadedMeta"`
	TotalSupply    int    `json:"totalSupply"`
	Status         string `json:"status"`
	MetaUploaded   bool   `json:"metaUploaded"`
	Root           string `json:"root"`
	TxHash         string `json:"txHash"`
}

func FetchMetaRootHash(addr string) (*MigrationInfo, error) {
	fields := `
migrations.id, 
migrations.addr, 
migrations.downloaded_meta, 
migrations.total_supply, 
status, 
root_indices.root AS root, 
CASE WHEN root_indices.uploaded_at IS NULL THEN 0 ELSE 1 END meta_uploaded,
root_indices.tx_hash AS tx_hash 
`
	var bean MigrationInfo
	ptr := &bean
	err := DB.Table("migrations").Select(fields).
		Joins("left join file_entries on migrations.meta_file_entry_id=file_entries.id").
		Joins("left join root_indices on file_entries.root_id=root_indices.id").
		Where("migrations.addr=?", addr).
		Take(&bean).Error
	if IsNotFound(err) {
		err = nil
		ptr = nil
	}
	return ptr, err
}

func CheckCache(ctx *gin.Context, root, name string) bool {
	// cache for 1 year
	ctx.Header("Cache-Control", "public, max-age=31536000")
	etag := root + "/" + name
	ctx.Header("ETag", etag)
	if match := ctx.GetHeader("If-None-Match"); match != "" {
		if match == etag {
			ctx.Status(http.StatusNotModified)
			return true
		}
	}
	return false
}
