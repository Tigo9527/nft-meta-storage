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
	"nft.house/service/db_models"
	"nft.house/service/query"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

const CtxUserId = "CTX_USER_ID"

func Routes(route *gin.Engine) {
	group := route.Group("/nft-house")
	group.Static("/static", "./web/static")
	group.GET("/meta/:id", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, group.BasePath()+"/static/meta/"+c.Param("id")+".json")
	})
	// for demo nft meta generation
	group.GET("/meta/robohash/:set/:id", func(c *gin.Context) {
		id := c.Param("id")
		set := c.Param("set")
		str := fmt.Sprintf(`{"name":"%s", "image":"%s"}`,
			"robohash#"+id, "https://robohash.org/"+id+"?set="+set)
		c.Writer.Header().Set("Content-Type", "application/json")
		_, _ = c.Writer.WriteString(str)
	})
	group.GET("/", api.Wrap(hello))
	group.POST("/store", authWrap(nftStore))
	// example path: /res/0x***
	group.GET("/res/:root", func(c *gin.Context) {
		service.ServeRawStorageFile(c, c.Param("root"))
	})
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
	group.GET("/delete-migration", api.Wrap(deleteMigration))
	group.GET("/abort-migration", api.Wrap(abortMigration))
	group.GET("/skip-download", api.Wrap(skipDownload))
	group.GET("/abort-uploading", api.Wrap(abortUploading))
}

// This is not a normal operation, but only for a test.
func deleteMigration(ctx *gin.Context) (interface{}, error) {
	m := query.Migration
	bean, err := m.Where(m.Addr.Eq(ctx.Query("addr"))).Take()
	if bean != nil {
		_, err = m.Delete(bean)
		_ = query.DeleteUrlCache(bean.Id)
		if err != nil {
			return nil, err
		}
	}
	return "ok", nil
}
func abortMigration(ctx *gin.Context) (interface{}, error) {
	m := query.Migration
	bean, err := m.Where(m.Addr.Eq(ctx.Query("addr"))).Take()
	if err != nil {
		return nil, err
	}
	if bean == nil {
		return nil, fmt.Errorf("not found")
	}
	ok := false
	currentUploadingId := service.CurrentFileId.Load()
	if bean.ImageFileEntryId > 0 && currentUploadingId == int64(bean.ImageFileEntryId) {
		service.AbortFileId.Store(currentUploadingId)
		ok = true
	} else if bean.MetaFileEntryId > 0 && currentUploadingId == int64(bean.MetaFileEntryId) {
		ok = true
		service.AbortFileId.Store(currentUploadingId)
	}
	if !ok {
		return nil, fmt.Errorf("task is not uploading right now")
	}
	_, _ = m.Delete(bean)
	_ = query.DeleteUrlCache(bean.Id)

	//fq := query.FileStoreQueue
	//info, err := fq.Where(fq.Id.In(int64(bean.MetaFileEntryId), int64(bean.ImageFileEntryId))).Delete()
	//return info, err

	return "submitted", nil
}
func abortUploading(ctx *gin.Context) (interface{}, error) {
	i, err := strconv.ParseInt(ctx.Query("id"), 10, 64)
	if err == nil {
		return nil, err
	}
	service.AbortFileId.Store(i)
	return "OK", nil
}
func skipDownload(ctx *gin.Context) (interface{}, error) {
	q := query.Migration
	update, err := q.Debug().Where(q.Addr.Eq(ctx.Query("addr"))).
		Where(q.Status.Eq(db_models.MigrationStatusDownload)).
		Update(q.Status, db_models.MigrationStatusPackImage)
	if err != nil {
		return nil, err
	}
	return update.RowsAffected, update.Error
}
func getMigrationResult(ctx *gin.Context) (interface{}, error) {
	result, err := service.FetchMetaRootHash(ctx.Query("addr"))
	return result, err
}

func addMigration(ctx *gin.Context) (interface{}, error) {
	now := time.Now()
	info := &db_models.Migration{
		Id:               0,
		Addr:             ctx.Query("addr"),
		ChainRpc:         ctx.Query("chainRpc"),
		ERC:              ctx.Query("erc"),
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
	var info db_models.Migration
	err := service.DB.Where("addr=?", ctx.Query("addr")).Take(&info).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return info, err
}

func resourceRequest(ctx *gin.Context) {
	root := ctx.Param("root")
	//name := ctx.Param("name")
	// meta may contains query string, will be used as file name when migrating download
	fullPath := ctx.Request.RequestURI
	name := fullPath[strings.LastIndex(fullPath, "/")+1:]
	ctx.Writer.Header().Set("res", name)

	if service.CheckCache(ctx, root, name) {
		return
	}

	if service.PatchResource(ctx, root, name) {
		return
	}

	ctx.Writer.Header().Set("file-source", "remote")

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

	rootArr, ok := mForm.Value["root"]
	if !ok || len(meta) != 1 {
		return nil, BuildError("invalid `root` in multipart")
	}
	root := rootArr[0]

	tokenIdArr, ok := mForm.Value["tokenId"]
	if !ok || len(meta) != 1 {
		return nil, BuildError("invalid `tokenId` in multipart")
	}
	tokenId := tokenIdArr[0]

	var result map[string]interface{}
	err = json.Unmarshal([]byte(meta[0]), &result)
	if err != nil {
		return nil, BuildError("can not unmarshal `meta` to a json")
	}

	now := time.Now()
	var fileEntries []*db_models.FileEntry
	//logrus.Debug("form file count ", len(mForm.File), " props", mForm)
	imgGatewayConf, err_ := query.Config.Where(query.Config.Name.Eq(db_models.ConfigImageGateway)).Take()
	err = err_
	if service.IsNotFound(err) || imgGatewayConf == nil || imgGatewayConf.Value == "" {
		err = fmt.Errorf("must config image gateway")
		return nil, err
	}
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

		filePath := fmt.Sprintf("./upload/%s_%s_%s", now.Format(time.RFC3339), field, oneFile.Filename)
		w, err := os.Create(filePath)
		if err != nil {
			return nil, BuildError("failed to create file")
		}

		_, err = io.Copy(w, readF)
		if err != nil {
			return nil, BuildError("failed to save file")
		}
		result[field] = fmt.Sprintf("%s/%s/%s",
			imgGatewayConf.Value, root, path.Base(filePath))

		fileEntries = append(fileEntries, &db_models.FileEntry{
			Id:        0,
			UserId:    ctx.GetInt64(CtxUserId),
			Name:      filePath,
			Size:      oneFile.Size,
			RootId:    0,
			CreatedAt: &now,
			UpdatedAt: &now,
		})
	}

	metaPath := fmt.Sprintf("./upload/%s_%s.json", now.Format(time.RFC3339), tokenId)
	metaWriter, err := os.Create(metaPath)
	if err != nil {
		return nil, err
	}
	metaBytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	_, err = metaWriter.Write(metaBytes)
	if err != nil {
		return nil, err
	}
	metaFileEntry := &db_models.FileEntry{
		Id:        0,
		UserId:    0,
		Name:      metaPath,
		Size:      int64(len(metaBytes)),
		RootId:    0,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	fileEntries = append(fileEntries, metaFileEntry)

	err = service.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Create(fileEntries).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to save file entries in DB")
		}
		var txArr []*db_models.FileTxQueue
		for _, entry := range fileEntries {
			txArr = append(txArr, &db_models.FileTxQueue{
				Id:        0, // auto increment
				CreatedAt: entry.CreatedAt,
				FileId:    entry.Id,
			})
		}
		err = tx.Create(txArr).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to save tx queue in DB")
		}
		var hex64 db_models.Hex64
		// save hex64
		err = tx.FirstOrCreate(&hex64, &db_models.Hex64{Hex: root}).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to save hex64 in DB")
		}
		// save resource map
		var resMap []*db_models.ResourceMap
		for _, entry := range fileEntries {
			resourceName := path.Base(entry.Name)
			if entry == metaFileEntry {
				resourceName = fmt.Sprintf("%s.json", tokenId)
			}
			resMap = append(resMap, &db_models.ResourceMap{
				Id:       0,
				HexId:    hex64.Id,
				Resource: resourceName,
				FileId:   entry.Id,
			})
		}
		return tx.Create(resMap).Error
	})
	if err != nil {
		logrus.WithError(err).Error("Failed to save infos to DB.")
		return nil, err
	}

	info := make(map[string]string)
	if img, ok := result["image"]; ok {
		info["image"] = img.(string)
	}
	info["meta"] = fmt.Sprintf("%s/%s/%s",
		// token uri in format prefix_url/{id},
		// which will be redirected to /res/{id}.json
		strings.Replace(imgGatewayConf.Value, "/res", "/storage/meta", -1),
		root, tokenId)
	return info, nil
}

func hello(ctx *gin.Context) (interface{}, error) {
	return "hello", nil
}
