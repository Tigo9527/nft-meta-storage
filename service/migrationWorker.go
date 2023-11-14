package service

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math/big"
	"nft.house/nft"
	"nft.house/service/db_models"
	"nft.house/service/query"
	"os"
	"path"
	"strings"
	"time"
)

func CheckMigrationTask() {
	for {
		lastId, err := GetIntConfig(db_models.ConfigDownloadingID, 0)
		if err != nil {
			logrus.WithError(err).Error("GetIntConfig failed")
			time.Sleep(10 * time.Second)
			continue
		}
		var bean db_models.Migration
		err = DB.Where("id >= ?", lastId).
			Order("id asc").
			Take(&bean).Error
		if IsNotFound(err) {
			//logrus.Debug("no more downloading task")
			time.Sleep(1 * time.Second)
			continue
		}
		if err != nil {
			logrus.WithError(err).Error("failed to get downloading task")
			time.Sleep(10 * time.Second)
			continue
		}

		if int(bean.Id) != lastId {
			err = SaveIntConfig(db_models.ConfigDownloadingID, int(bean.Id))
			if err != nil {
				logrus.WithError(err).Error("failed to SaveIntConfig")
				time.Sleep(10 * time.Second)
				continue
			}
		}
		switch bean.Status {
		case db_models.MigrationStatusDownload:
			err = download(&bean)
			break
		case db_models.MigrationStatusPackImage:
			err = nft.PackImage(fmt.Sprintf("./download/%s", bean.Addr))
			if err == nil {
				err = DB.Model(bean).Update("status", db_models.MigrationStatusPackMeta).Error
			}
			break
		case db_models.MigrationStatusPackMeta:
			imgGatewayConf, err_ := query.Config.Where(query.Config.Name.Eq(db_models.ConfigImageGateway)).Take()
			err = err_
			if IsNotFound(err) || imgGatewayConf == nil || imgGatewayConf.Value == "" {
				err = fmt.Errorf("must config image gateway")
			}
			if err != nil {
				break
			}
			nftDir := fmt.Sprintf("./download/%s", bean.Addr)
			err = nft.ReplaceImageInMeta(nftDir, imgGatewayConf.Value, bean.Id)
			if err != nil {
				break
			}
			err = nft.PackMeta(nftDir)
			if err == nil {
				err = DB.Model(bean).Update("status", db_models.MigrationStatusUploadImage).Error
			}
			break
		case db_models.MigrationStatusUploadImage:
			filepath := fmt.Sprintf("./download/%s/%s", bean.Addr, nft.ImageData)
			fileId, err_ := addUploadTask(filepath)
			err = err_
			if err == nil {
				err = DB.Model(bean).
					Update("status", db_models.MigrationStatusWaitUploadingImage).
					Update("image_file_entry_id", fileId).
					Error
			}
			break
		case db_models.MigrationStatusWaitUploadingImage:
			err = checkUpload(&bean, db_models.MigrationStatusUploadMeta)
			break
		case db_models.MigrationStatusUploadMeta:
			filepath := fmt.Sprintf("./download/%s/%s", bean.Addr, nft.MetaData)
			fileId, err_ := addUploadTask(filepath)
			err = err_
			if err == nil {
				err = DB.Model(bean).
					Update("status", db_models.MigrationStatusWaitUploadingMeta).
					Update("meta_file_entry_id", fileId).
					Error
			}
			break
		case db_models.MigrationStatusWaitUploadingMeta:
			err = checkUpload(&bean, db_models.MigrationStatusFinished)
			break
		case db_models.MigrationStatusFinished:
			err = SaveIntConfig(db_models.ConfigDownloadingID, int(bean.Id+1))
			break
		default:
			err = fmt.Errorf("unknow status %s", bean.Status)
			break
		}
		if err != nil {
			logrus.WithError(err).Error("migration failed")
			time.Sleep(10 * time.Second)
			continue
		}
	}
}

func checkUpload(bean *db_models.Migration, next string) error {
	isImage := next == db_models.MigrationStatusUploadMeta
	fileEntryId := bean.ImageFileEntryId
	uploadedKey := "image_uploaded"
	if !isImage {
		fileEntryId = bean.MetaFileEntryId
		uploadedKey = "meta_uploaded"
	}
	var fileEntry db_models.FileEntry
	err := DB.Where("id=?", fileEntryId).Take(&fileEntry).Error
	if err != nil {
		if IsNotFound(err) {
			err = fmt.Errorf("file entry is lost %d", fileEntryId)
		}
		return err
	}
	if fileEntry.RootId <= 0 {
		err = fmt.Errorf("waiting for create root index, file id %d", fileEntryId)
		return err
	}
	var rootIndex db_models.RootIndex
	err = DB.Where("id=?", fileEntry.RootId).Take(&rootIndex).Error
	if err != nil {
		if IsNotFound(err) {
			err = fmt.Errorf("root index not ready %d", fileEntry.RootId)
		}
		return err
	}
	info, err := ctx.Storage.Neurahive().GetFileInfo(common.HexToHash(rootIndex.Root))
	if err != nil {
		return err
	}
	if info == nil {
		err = fmt.Errorf("file not found at Storage for now: %s", rootIndex.Root)
	} else if !info.Finalized {
		err = fmt.Errorf("file not finalized at Storage for now: %s", rootIndex.Root)
	} else {
		err = DB.Model(bean).
			Update("status", next).
			Update(uploadedKey, true).
			Error
	}
	return err
}

func addUploadTask(filepath string) (int64, error) {
	stat, err := os.Stat(filepath)
	if err != nil {
		return 0, err
	}
	fileEntry := db_models.FileEntry{
		Id:        0,
		UserId:    0,
		Name:      filepath,
		Size:      stat.Size(),
		RootId:    0,
		CreatedAt: nil,
		UpdatedAt: nil,
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		dbE := tx.Create(&fileEntry).Error
		if dbE == nil {
			dbE = tx.Create(&db_models.FileTxQueue{
				Id:        0,
				FileId:    fileEntry.Id,
				CreatedAt: nil,
			}).Error
		}
		return dbE
	})
	return fileEntry.Id, err
}

func download(bean *db_models.Migration) error {
	logrus.Debug("download ", bean.Addr)
	err := nft.Setup(bean.ChainRpc)
	if err != nil {
		return err
	}
	ctx, err := nft.BuildERC721Enumerable(bean.Addr)
	if err != nil {
		logrus.WithError(err).Error("failed to BuildERC721Enumerable")
		return err
	}
	if bean.TotalSupply != int(ctx.TotalSupply.Int64()) {
		bean.Name = ctx.Name
		bean.TotalSupply = int(ctx.TotalSupply.Int64())
		err = DB.Model(bean).Update("total_supply", ctx.TotalSupply.Int64()).
			Update("name", ctx.Name).Error
		if err != nil {
			return errors.WithMessage(err, "failed to update totalSupply and name")
		}
	}
	saveAtDir := fmt.Sprintf("./download/%s", bean.Addr)
	if _, err = os.Stat(saveAtDir); err != nil {
		if os.IsNotExist(err) {
			// that's ok
		} else {
			// permission or what ever error
			return err
		}
	} else {
		// delete it
		err = os.RemoveAll(saveAtDir)
		if err != nil {
			return err
		}
	}
	err = os.MkdirAll(saveAtDir, 0755)
	if err != nil {
		return errors.WithMessage(err, "mkdirAll")
	}
	for {
		log := logrus.WithFields(logrus.Fields{"contract": bean.Addr, "name": bean.Name})
		if bean.DownloadedMeta >= bean.TotalSupply {
			logrus.Infof("%d >= %d , stop downloading\n", bean.DownloadedMeta, bean.TotalSupply)
			err = DB.Model(bean).Update("status", db_models.MigrationStatusPackImage).Error
			if err != nil {
				return errors.WithMessage(err, "failed to update status")
			}
			break
		}
		tokenId, err := ctx.Caller.TokenByIndex(nft.CallOpts, big.NewInt(int64(bean.DownloadedMeta)))
		if err != nil {
			log.WithField("TokenByIndex", bean.DownloadedMeta).Error("error context")
			return errors.WithMessage(err, "failed to call TokenByIndex")
		}

		uri, err := ctx.Caller.TokenURI(nft.CallOpts, tokenId)
		if err != nil {
			log.WithField("TokenURI", tokenId).Error("error context")
			return errors.WithMessage(err, "failed to call TokenURI")
		}

		url := nft.FormatUri(&uri, tokenId)
		log.Debug("download meta from ", *url)
		metaFile := fmt.Sprintf("%s/%s.json", saveAtDir, tokenId)
		_, err = nft.Download(*url, metaFile)
		if err != nil {
			log.WithField("metaUrl", *url).
				WithField("saveAtDir", saveAtDir).Error("Download")
			return errors.WithMessage(err, "download meta error")
		}

		imgUrl, err := nft.ParseImage(metaFile)
		if err != nil {
			return errors.WithMessage(err, "failed to parse image, meta file "+metaFile)
		}

		localName, err := query.GetCachedUrl(bean.Id, imgUrl)
		if err != nil {
			return err
		}
		// download if local cache is empty
		if localName == "" {
			filename := imgUrl[strings.LastIndex(imgUrl, "/")+1:]
			imgDst := fmt.Sprintf("%s/%s.image.%s", saveAtDir, tokenId, filename)
			_, err = nft.Download(imgUrl, imgDst)
			if err != nil {
				return errors.WithMessage(err, "failed to download image "+imgUrl)
			}
			err := query.UrlEntry.Save(&db_models.UrlEntry{
				Id:          0,
				MigrationId: bean.Id,
				Url:         imgUrl,
				LocalName:   path.Base(imgDst),
			})
			if err != nil {
				return err
			}
		}
		//update progress
		bean.DownloadedMeta += 1
		err = DB.Model(bean).Update("downloaded_meta", bean.DownloadedMeta).Error
		if err != nil {
			return errors.WithMessage(err, "failed to update DownloadedMeta count")
		}
		log.Debug("downloaded ", bean.DownloadedMeta)

		// check status changing by external
		m := query.Migration
		newBean, err := m.Where(m.Addr.Eq(bean.Addr)).Select(m.Status).Take()
		if err != nil {
			return err
		}
		if newBean.Status != db_models.MigrationStatusDownload {
			break
		}
	}
	return nil
}
