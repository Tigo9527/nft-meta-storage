package service

import (
	"github.com/Conflux-Chain/neurahive-client/common/blockchain"
	"github.com/Conflux-Chain/neurahive-client/contract"
	"github.com/Conflux-Chain/neurahive-client/file"
	"github.com/Conflux-Chain/neurahive-client/node"
	"github.com/ethereum/go-ethereum/common"
	"github.com/openweb3/web3go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/gorm"
	"io"
	"nft.house/nft"
	"time"
)

var storeTimer = time.NewTicker(time.Second)

type StorageContext struct {
	Client   *web3go.Client
	Storage  *node.Client
	uploader *file.Uploader
}

func RunUploadWorker() {
	ctx := SetupContext()

	for {
		select {
		case <-storeTimer.C:
			CheckUploadQueue(ctx)
		}
	}
}

func SetupContext() *StorageContext {
	blockchainUrl := viper.GetString("blockchain.url")
	contractStr := viper.GetString("blockchain.contract")
	pk := viper.GetString("blockchain.pk")
	storageNode := viper.GetString("storage.node")
	contractAddr := common.HexToAddress(contractStr)
	client := blockchain.MustNewWeb3(blockchainUrl, pk)

	flow, _ := contract.NewFlowContract(contractAddr, client)

	chainId, _ := client.Eth.ChainId()
	signer, _ := blockchain.DefaultSigner(client)
	balance, _ := client.Eth.Balance(signer.Address(), nil)
	logrus.WithFields(logrus.Fields{
		"balance": balance.String(), "chainId": *chainId, "account": signer.Address(),
	}).Info("chain id ", *chainId, "")

	nodeInst := node.MustNewClient(storageNode)
	logrus.Info("storage node ", storageNode)
	logrus.Info("flow contract ", contractStr)

	uploader := file.NewUploader(flow, nodeInst)
	ctx := &StorageContext{
		Client:   client,
		Storage:  nodeInst,
		uploader: uploader,
	}
	return ctx
}

func CheckUploadQueue(ctx *StorageContext) {
	var task FileStoreQueue
	err := DB.Order("id asc").Take(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		logrus.WithError(err).Error("Failed to take FileStoreQueue")
		return
	}
	runTask(&task, ctx)
}

func runTask(task *FileStoreQueue, ctx *StorageContext) {
	logWithFields := logrus.WithFields(logrus.Fields{
		"taskId": task.Id, "rootId": task.RootId,
	})
	var rootIndex RootIndex
	err := DB.Where("id=?", task.RootId).Take(&rootIndex).Error
	if err != nil {
		logWithFields.WithError(err).Error("failed to upload, root index error")
		return
	}
	if task.Step == UploadStepUploading {
		var fileEntry FileEntry
		err = DB.Where("id=?", rootIndex.FileId).Take(&fileEntry).Error
		if err != nil {
			logWithFields.WithError(err).Error("failed to upload, file entry error")
			return
		}
		logrus.Debug("upload to storage: ", fileEntry.Name)
		err = ctx.uploader.UploadStep(fileEntry.Name)
		if err != nil {
			logWithFields.WithError(err).Error("failed to execute uploading")
			return
		}
		logrus.Debug("uploaded to node ", ctx.Storage.URL())
		err = recreateTask(task)
		if err != nil {
			logWithFields.WithError(err).Error("failed to cleanup ")
			return
		}
	} else {
		info, err := ctx.Storage.Neurahive().GetFileInfo(common.HexToHash(rootIndex.Root))
		if err != nil {
			logWithFields.WithError(err).Error("upload: failed to get file info")
		}
		if info == nil || !info.Finalized {
			logWithFields.Debug("info is nil or not finalized ", info,
				"root ", rootIndex.Root, " hash ", common.HexToHash(rootIndex.Root))
			err = recreateTask(task)
			if err != nil {
				logWithFields.WithError(err).Error(err, "waitConfirm: failed to recreate task")
				return
			}
		} else {
			err = DB.Transaction(func(tx *gorm.DB) error {
				tx.Model(&rootIndex).Update("uploaded_at", time.Now())
				tx.Delete(task)
				return nil
			})
			if err != nil {
				logWithFields.WithError(err).Error(err, "failed to confirm upload in DB")
				return
			}
			logWithFields.Info("upload confirmed.")
		}
	}
}

func recreateTask(task *FileStoreQueue) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Delete(task).Error
		if err != nil {
			return errors.WithMessage(err, "uploaded: failed to delete task")
		}
		task.Id = 0
		task.Step = UploadStepWaitConfirm
		err = tx.Create(task).Error
		if err != nil {
			return errors.WithMessage(err, "uploaded: failed to recreate task")
		}
		return nil
	})
}

func ServeFileFromStorage(root string, name string, writer io.Writer) error {
	return nft.GetFileByIndex(ctx.Storage, root, name, writer)
}
