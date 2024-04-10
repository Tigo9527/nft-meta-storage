package service

import (
	"github.com/Conflux-Chain/neurahive-client/common/blockchain"
	"github.com/Conflux-Chain/neurahive-client/contract"
	"github.com/Conflux-Chain/neurahive-client/file"
	"github.com/Conflux-Chain/neurahive-client/node"
	"github.com/ethereum/go-ethereum/common"
	"github.com/openweb3/go-rpc-provider/provider_wrapper"
	"github.com/openweb3/web3go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/net/context"
	"gorm.io/gorm"
	"io"
	"nft.house/nft"
	"nft.house/service/db_models"
	"strings"
	"sync/atomic"
	"time"
)

var storeTimer = time.NewTicker(time.Second)
var AbortFileId atomic.Int64
var CurrentFileId atomic.Int64
var abortError = errors.New("aborted")

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
	nodeInst.MiddlewarableProvider.HookCallContext(func(f providers.CallContextFunc) providers.CallContextFunc {
		return func(ctx context.Context, resultPtr interface{}, method string, args ...interface{}) error {
			if method == "nrhv_uploadSegment" {
				if CurrentFileId.Load() == AbortFileId.Load() {
					return abortError
				}
			}
			err := f(ctx, resultPtr, method, args...)

			return err
		}
	})
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
	var task db_models.FileStoreQueue
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

func runTask(task *db_models.FileStoreQueue, ctx *StorageContext) {
	logWithFields := logrus.WithFields(logrus.Fields{
		"taskId": task.Id, "rootId": task.RootId,
	})
	var rootIndex db_models.RootIndex
	err := DB.Where("id=?", task.RootId).Take(&rootIndex).Error
	if err != nil {
		logWithFields.WithError(err).Error("failed to upload, root index error")
		return
	}
	if task.Step == db_models.UploadStepUploading && rootIndex.UploadedAt == nil {
		var fileEntry db_models.FileEntry
		err = DB.Where("id=?", rootIndex.FileId).Take(&fileEntry).Error
		if err != nil {
			logWithFields.WithError(err).Error("failed to upload, file entry error")
			return
		}
		logrus.Debug("upload to storage: ", fileEntry.Name)
		CurrentFileId.Store(task.Id)
		defer func() { CurrentFileId.Store(0) }()
		err = ctx.uploader.UploadStep(fileEntry.Name)
		if err != nil {
			if errors.Is(err, abortError) {
				logrus.Warnf("abrot upload task %d", AbortFileId.Load())
				DB.Delete(task)
				AbortFileId.Store(0)
				return
			} else if strings.Index(err.Error(), "already uploaded and finalized") < 0 {
				logWithFields.WithError(err).Error("failed to execute uploading")
				return
			}
		}
		logrus.Debug("uploaded to node ", ctx.Storage.URL())
		err = recreateTaskWaitConfirm(task)
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
			err = recreateTaskWaitConfirm(task)
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

func recreateTaskWaitConfirm(task *db_models.FileStoreQueue) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Delete(task).Error
		if err != nil {
			return errors.WithMessage(err, "uploaded: failed to delete task")
		}
		task.Id = 0
		task.Step = db_models.UploadStepWaitConfirm
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
