package web

import (
	"encoding/json"
	"fmt"
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"io"
	"net/http"
	"nft.house/nft"
	"nft.house/service"
	"os"
	"time"
)

const CtxUserId = "CTX_USER_ID"

func Routes(route *gin.Engine) {
	group := route.Group("/nft-house")
	group.Static("/static", "./web/static")
	group.GET("/meta/:id", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, group.BasePath()+"/static/meta/"+c.Param("id")+".json")
	})
	group.GET("/", api.Wrap(hello))
	group.POST("/store", authWrap(nftStore))
	// example path: /res/0x***/1.json
	group.GET("/res/:root/:name", resourceRequest)
	// append .json, and redirect
	group.GET("/storage/meta/:root/:id", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, group.BasePath()+
			"/res/"+c.Param("root")+"/"+c.Param("id")+".json")
	})

	group.GET("/migration-info", api.Wrap(migrationInfo))
	group.GET("/migration-result", api.Wrap(getMigrationResult))
	group.POST("/add-migration", api.Wrap(addMigration))
}

func getMigrationResult(ctx *gin.Context) (interface{}, error) {
	result, err := service.FetchMetaRootHash(ctx.Query("addr"))
	return result, err
}

func addMigration(ctx *gin.Context) (interface{}, error) {
	now := time.Now()
	info := &service.Migration{
		Id:               0,
		Addr:             ctx.Query("addr"),
		ChainRpc:         ctx.Query("chainRpc"),
		TotalSupply:      0,
		DownloadedMeta:   0,
		Status:           "download",
		ImageFileEntryId: 0,
		MetaFileEntryId:  0,
		ImageUploaded:    false,
		MetaUploaded:     false,
		Name:             ctx.Query("name"),
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}
	err := service.DB.Create(info).Error
	return *info, err
}
func migrationInfo(ctx *gin.Context) (interface{}, error) {
	var info service.Migration
	err := service.DB.Where("addr=?", ctx.Query("addr")).Take(&info).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return info, err
}

func resourceRequest(ctx *gin.Context) {
	root := ctx.Param("root")
	name := ctx.Param("name")

	err := service.ServeFileFromStorage(root, name, ctx.Writer)
	logrus.WithError(err).Debug("ServeFileFromStorage")
	if err != nil {
		if errors.Is(err, &nft.ErrorFileNotFound{}) {
			ctx.Status(http.StatusNotFound)
			return
		} else if errors.Is(err, nft.ErrorFileEntryNotFound) {
			ctx.String(http.StatusBadRequest, err.Error())
			return
		}
		ctx.Status(http.StatusInternalServerError)
		_, _ = ctx.Writer.WriteString(err.Error())
		return
	}
}

func authWrap(controller func(c *gin.Context) (interface{}, error)) gin.HandlerFunc {
	h := api.Wrap(controller)
	return func(c *gin.Context) {
		//accessKey := c.GetHeader("accessKey")
		//if accessKey == "" {
		//	api.ResponseError(c, BuildError("required: accessKey in header."))
		//	return
		//}
		//var user service.User
		//err := service.DB.Where("token=?", accessKey).Take(&user).Error
		//if err != nil {
		//	if errors.Is(err, gorm.ErrRecordNotFound) {
		//		api.ResponseError(c, BuildError("invalid accessKey"))
		//		return
		//	}
		//	api.ResponseError(c, BuildError("invalid accessKey: %s", err.Error()))
		//	return
		//}
		//c.Set(CtxUserId, user.Id)
		c.Set(CtxUserId, 1)
		h(c)
	}
}

func BuildError(format string, args ...interface{}) *api.BusinessError {
	return api.NewBusinessError(api.ErrCodeValidation, fmt.Sprintf(format, args...), nil)
}

func nftStore(ctx *gin.Context) (interface{}, error) {
	contentType := ctx.ContentType()
	if contentType != "multipart/form-data" {
		return nil, BuildError("Content-Type should be `multipart/form-data`")
	}
	mForm, err := ctx.MultipartForm()
	if err != nil {
		return nil, BuildError("parse MultipartForm error, please check your request: %v", err)
	}

	meta, ok := mForm.Value["meta"]
	if !ok || len(meta) != 1 {
		return nil, BuildError("invalid `meta` in multipart")
	}

	var result map[string]interface{}
	err = json.Unmarshal([]byte(meta[0]), &result)
	if err != nil {
		return nil, BuildError("can not unmarshal `meta` to a json")
	}

	var fileEntries []*service.FileEntry
	logrus.Debug("form file count ", len(mForm.File))
	for field, f := range mForm.File {
		if len(f) != 1 {
			return nil, BuildError("file field %v should (only) have 1 element", field)
		}
		oneFile := f[0]
		logrus.WithFields(logrus.Fields{
			"file":  oneFile.Filename,
			"field": field,
			"size":  oneFile.Size}).Debug("received file")

		readF, err := oneFile.Open()
		if err != nil {
			return nil, BuildError("can not open file %v", field)
		}

		now := time.Now()
		filePath := fmt.Sprintf("./upload/%s_%s_%s", now.Format(time.RFC3339), field, oneFile.Filename)
		w, err := os.Create(filePath)
		if err != nil {
			return nil, BuildError("failed to create file")
		}

		_, err = io.Copy(w, readF)
		if err != nil {
			return nil, BuildError("failed to save file")
		}
		result[field] = filePath

		fileEntries = append(fileEntries, &service.FileEntry{
			Id:        0,
			UserId:    ctx.GetInt64(CtxUserId),
			Name:      filePath,
			Size:      oneFile.Size,
			RootId:    0,
			CreatedAt: &now,
			UpdatedAt: &now,
		})
	}

	if len(fileEntries) > 0 {
		err = service.DB.Transaction(func(tx *gorm.DB) error {
			err := service.DB.Create(fileEntries).Error
			if err != nil {
				return errors.WithMessage(err, "Failed to save file entries in DB")
			}
			var txArr []*service.FileTxQueue
			for _, entry := range fileEntries {
				txArr = append(txArr, &service.FileTxQueue{
					Id:        0, // auto increment
					CreatedAt: entry.CreatedAt,
					FileId:    entry.Id,
				})
			}
			err = service.DB.Create(txArr).Error
			if err != nil {
				return errors.WithMessage(err, "Failed to save tx queue in DB")
			}
			return nil
		})
		if err != nil {
			logrus.WithError(err).Error("Failed to save infos to DB.")
			return nil, err
		}
	}

	return result, nil
}

func hello(ctx *gin.Context) (interface{}, error) {
	return "hello", nil
}
