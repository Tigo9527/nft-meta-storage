package service

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math/big"
	"nft.house/nft"
	"os"
	"strings"
	"time"
)

func CheckMigrationTask() {
	for {
		lastId, err := GetIntConfig(ConfigDownloadingID, 0)
		if err != nil {
			logrus.WithError(err).Error("GetIntConfig failed")
			time.Sleep(10 * time.Second)
			continue
		}
		var bean Migration
		err = DB.Where("id >= ?", lastId).
			Order("id asc").
			Take(&bean).Error
		if IsNotFound(err) {
			logrus.Debug("no more downloading task")
			time.Sleep(10 * time.Second)
			continue
		}
		if err != nil {
			logrus.WithError(err).Error("failed to get downloading task")
			time.Sleep(10 * time.Second)
			continue
		}

		if int(bean.Id) != lastId {
			err = SaveIntConfig(ConfigDownloadingID, int(bean.Id))
			if err != nil {
				logrus.WithError(err).Error("failed to SaveIntConfig")
				time.Sleep(10 * time.Second)
				continue
			}
		}
		switch bean.Status {
		case MigrationStatusDownload:
			err = download(&bean)
			break
		case MigrationStatusPackImage:
			err = nft.PackImage(fmt.Sprintf("./download/%s", bean.Addr))
			if err == nil {
				err = DB.Model(bean).Update("status", MigrationStatusPackMeta).Error
			}
			break
		case MigrationStatusPackMeta:
			err = nft.PackMeta(fmt.Sprintf("./download/%s", bean.Addr))
			if err == nil {
				err = DB.Model(bean).Update("status", MigrationStatusUploadImage).Error
			}
			break
		case MigrationStatusUploadImage:
			filepath := fmt.Sprintf("./download/%s/image.data", bean.Addr)
			fileId, err := addUploadTask(filepath)
			if err == nil {
				err = DB.Model(bean).
					Update("status", MigrationStatusWaitUploadingImage).
					Update("image_file_entry_id", fileId).
					Error
			}
			break
		case MigrationStatusWaitUploadingImage:
			err = checkUpload(&bean, MigrationStatusUploadMeta)
			break
		case MigrationStatusUploadMeta:
			filepath := fmt.Sprintf("./download/%s/meta.data", bean.Addr)
			fileId, err := addUploadTask(filepath)
			if err == nil {
				err = DB.Model(bean).
					Update("status", MigrationStatusWaitUploadingMeta).
					Update("meta_file_entry_id", fileId).
					Error
			}
			break
		case MigrationStatusWaitUploadingMeta:
			err = checkUpload(&bean, MigrationStatusFinish)
			break
		case MigrationStatusFinish:
			err = SaveIntConfig(ConfigDownloadingID, int(bean.Id+1))
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

func checkUpload(bean *Migration, next string) error {
	isImage := next == MigrationStatusUploadMeta
	fileEntryId := bean.ImageFileEntryId
	uploadedKey := "image_uploaded"
	if !isImage {
		fileEntryId = bean.MetaFileEntryId
		uploadedKey = "meta_uploaded"
	}
	var fileEntry FileEntry
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
	var rootIndex RootIndex
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
	fileEntry := FileEntry{
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
			dbE = tx.Create(&FileTxQueue{
				Id:        0,
				FileId:    fileEntry.Id,
				CreatedAt: nil,
			}).Error
		}
		return dbE
	})
	return fileEntry.Id, err
}

func download(bean *Migration) error {
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
	err = os.MkdirAll(saveAtDir, 0755)
	if err != nil {
		return errors.WithMessage(err, "mkdirAll")
	}
	for {
		log := logrus.WithFields(logrus.Fields{"contract": bean.Addr, "name": bean.Name})
		if bean.DownloadedMeta >= bean.TotalSupply {
			logrus.Infof("%d >= %d , stop downloading\n", bean.DownloadedMeta, bean.TotalSupply)
			err = DB.Model(bean).Update("status", MigrationStatusPackImage).Error
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
		filename := imgUrl[strings.LastIndex(imgUrl, "/")+1:]
		imgDst := fmt.Sprintf("%s/%s.image.%s", saveAtDir, tokenId, filename)
		_, err = nft.Download(imgUrl, imgDst)
		if err != nil {
			return errors.WithMessage(err, "failed to download image "+imgUrl)
		}

		//update progress
		bean.DownloadedMeta += 1
		err = DB.Model(bean).Update("downloaded_meta", bean.DownloadedMeta).Error
		if err != nil {
			return errors.WithMessage(err, "failed to update DownloadedMeta count")
		}
		log.Debug("downloaded ", bean.DownloadedMeta)
	}
	return nil
}
