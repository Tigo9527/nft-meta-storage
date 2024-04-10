package service

import (
	"github.com/Conflux-Chain/neurahive-client/file"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"nft.house/service/db_models"
	"time"
)

var PaymentChan = make(chan int64)
var timer = time.NewTicker(time.Second)

func CheckPaymentTask() {
	for {
		select {
		case <-timer.C:
			CheckByOrderInDB()
		case id := <-PaymentChan:
			CheckById(id)
		}
	}
}

func CheckById(id int64) {
	var txTask db_models.FileTxQueue
	err := DB.Where("id=?", id).Take(&txTask).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		logrus.WithError(err).Error("Failed to take FileTxQueue.")
		return
	}
	CheckByTask(&txTask)
}

func CheckByTask(txTask *db_models.FileTxQueue) {
	var fileEntry db_models.FileEntry
	err := DB.Where("id=?", txTask.FileId).Take(&fileEntry).Error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"fileEntryId": txTask.FileId,
		}).WithError(err).Error("CheckByTask: Failed to take FileEntry")
		return
	}

	if fileEntry.RootId == 0 {
		createRoot(&fileEntry, txTask)
		return
	}
	var rootIndex db_models.RootIndex
	err = DB.Where("id=?", fileEntry.RootId).Take(&rootIndex).Error
	if err != nil {
		// must find if rootId is set on file entry
		logrus.WithFields(logrus.Fields{
			"fileId": fileEntry.Id, "rootId": fileEntry.RootId,
		}).WithError(err).Error("root index not found")
		return
	}

	// send tx
	now := time.Now()
	if rootIndex.TxHash == "" {
		wrappedFile, openErr := file.Open(fileEntry.Name)
		if openErr != nil {
			logrus.WithError(openErr).Error("Failed to open file")
			return
		}

		logrus.Debug("call SubmitLogEntry...")
		rcpt, err := ctx.uploader.SubmitLogEntry(wrappedFile, []byte{})
		if err != nil {
			logrus.WithError(err).Error("Failed to submit log entry")
			return
		}
		logrus.Info("SubmitLogEntry tx ", rcpt.TransactionHash.Hex())

		logrus.Debug("wait for log entry...")
		tree, _ := wrappedFile.MerkleTree()
		err = ctx.uploader.WaitForLogEntry(tree.Root(), false)

		err = DB.Transaction(func(tx *gorm.DB) error {
			err = DB.Model(&rootIndex).
				Updates(map[string]interface{}{
					"tx_hash": rcpt.TransactionHash.Hex(),
					"paid_at": &now,
				}).Error
			if err != nil {
				return errors.WithMessage(err, "Failed to update tx hash ")
			}
			return moveTaskToTail(tx, txTask)
		})
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"fileId": fileEntry.Id, "rootId": fileEntry.RootId,
			}).WithError(err).Error("")
		} else {
			logrus.Debug("created layer1 tx")
		}
		return
	}
	// check receipt
	// put into upload queue
	err = DB.Transaction(func(tx *gorm.DB) error {
		err = DB.Delete(txTask).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to delete task ")
		}
		err = DB.Create(&db_models.FileStoreQueue{
			Id:        fileEntry.Id,
			RootId:    rootIndex.Id,
			Step:      db_models.UploadStepUploading,
			CreatedAt: &now,
		}).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to create uploading task ")
		}
		return nil
	})
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"fileId": fileEntry.Id, "rootId": fileEntry.RootId,
		}).WithError(err).Error("")
		return
	}
	logrus.WithFields(logrus.Fields{
		"fileId": fileEntry.Id, "rootId": fileEntry.RootId,
	}).Info("created storage task")
}

func createRoot(entry *db_models.FileEntry, task *db_models.FileTxQueue) {
	wrappedFile, openErr := file.Open(entry.Name)
	if openErr != nil {
		logrus.WithError(openErr).Error("failed to open file")
		return
	}
	tree, merkleErr := wrappedFile.MerkleTree()
	if merkleErr != nil {
		logrus.WithError(merkleErr).Error("failed to build merkle tree")
		return
	}
	root := tree.Root()
	var rootIndex db_models.RootIndex
	err := DB.Where("root=?", root.Hex()).Take(&rootIndex).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logrus.WithFields(logrus.Fields{
				"root": root, "file": entry.Name,
			}).Error("failed to query root index by ROOT")
			return
		}
	} else {
		logrus.WithFields(logrus.Fields{
			"root": root, "file": entry.Name, "rootId": rootIndex.Id,
			"fileId": entry.Id,
		}).Info("root already exists")

		err = DB.Transaction(func(tx *gorm.DB) error {
			//err = tx.Delete(task).Error
			//if err != nil {
			//	return errors.WithMessage(err, "Failed to Delete task")
			//}
			err = tx.Model(&entry).Update("root_id", rootIndex.Id).Error
			if err != nil {
				return errors.WithMessage(err, "Failed to update root id on fileEntry")
			}
			return moveTaskToTail(tx, task)
		})
		if err != nil {
			logrus.WithError(err).Error("Failed to append task")
		}
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		rootIndex = db_models.RootIndex{
			Id:         0,
			Root:       root.Hex(),
			FileId:     entry.Id,
			TxHash:     "",
			PaidAt:     nil,
			UploadedAt: nil,
			CreatedAt:  nil,
			UpdatedAt:  nil,
		}
		err := DB.Create(&rootIndex).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to create root index ")
		}

		err = DB.Model(&entry).Update("root_id", rootIndex.Id).Error
		if err != nil {
			return errors.WithMessage(err, "Failed to update root id on FileEntry ")
		}

		return moveTaskToTail(tx, task)
	})
	if err != nil {
		logrus.WithError(err).Error("")
		return
	}
	logrus.WithFields(logrus.Fields{
		"root": root, "file": entry.Name, "rootId": rootIndex.Id,
		"fileId": entry.Id,
	}).Info("New root index created")
}

func moveTaskToTail(tx *gorm.DB, task *db_models.FileTxQueue) error {
	err := tx.Delete(task).Error
	if err != nil {
		return errors.WithMessage(err, "Failed to delete old task")
	}

	task.Id = 0
	// move it to the tail of the queue
	err = tx.Create(task).Error
	if err != nil {
		return errors.WithMessage(err, "Failed to create new task ")
	}
	return nil
}

func CheckByOrderInDB() {
	var bean db_models.FileTxQueue
	err := DB.Order("id asc").Take(&bean).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		logrus.WithError(err).Error("Failed to query FileTxQueue")
		return
	}
	CheckByTask(&bean)
}
