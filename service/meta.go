package service

import (
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/Conflux-Chain/neurahive-client/file"
	"github.com/Conflux-Chain/neurahive-client/node"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
	"nft.house/service/query"
	"os"
	"time"
)

// upload image and meta
// compute image hash,
// fill meta with image hash
// compute meta hash
// bind contract+tokenId=>storageGateway/meta_hash
// maybe pack layer1 txs  together.
// upload image and meta.
// how to handle resource request when they are not uploaded to storage ?

/*
Patch packed data:
Track hash/id in DB, if hit, use it, else fetch from storage
DB keeps mapping,
	A: to a local file
	B: to a storage ref
*/

type ResourceInfo struct {
	HexId      int64
	FileId     int64
	LocalPath  string
	RootId     int64
	Root       string
	UploadedAt *time.Time
}

// FineLocalResource return localPath, root, error
func FineLocalResource(resRoot, name string) (string, string, error) {
	hex64 := query.Hex64
	resMap := query.ResourceMap
	fe := query.FileEntry
	root := query.RootIndex
	var resInfo ResourceInfo
	err := hex64.
		Debug().
		Select(hex64.Id.As("hex_id"),
			resMap.FileId.As("file_id"),
			fe.Name.As("local_path"),
			fe.RootId.As("root_id"),
			root.Root.As("root"),
			root.UploadedAt.As("uploaded_at"),
		).LeftJoin(resMap, resMap.HexId.EqCol(hex64.Id), resMap.Resource.Eq(name)).
		LeftJoin(fe, fe.Id.EqCol(resMap.FileId)).
		LeftJoin(root, root.Id.EqCol(fe.RootId)).
		Where(hex64.Hex.Eq(resRoot)).Scan(&resInfo)
	logrus.WithError(err).WithField("result", resInfo).Debug("resource info")
	if err != nil {
		return "", "", err
	}
	_, err = os.Stat(resInfo.LocalPath)
	if err != nil {
		// local file failed, check uploaded info
		logrus.WithField("path", resInfo.LocalPath).Debug("file not exists ")
		return "", resInfo.Root, nil
	}
	return resInfo.LocalPath, resInfo.Root, nil
}

func PipeLocalFile(localPath string, w io.Writer) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	if err != nil {
		logrus.WithError(err).WithField("localPath", localPath).
			Error("pipe local file error")
	}
	return err
}

func PatchResource(c *gin.Context, resRoot, name string) bool {
	localPath, newRoot, err := FineLocalResource(resRoot, name)
	if err != nil {
		api.ResponseError(c, err)
	} else if localPath != "" {
		err := PipeLocalFile(localPath, c.Writer)
		if err != nil {
			api.ResponseError(c, err)
		}
	} else if newRoot != "" {
		info, err := ctx.Storage.Neurahive().GetFileInfo(common.HexToHash(newRoot))
		if err != nil {
			api.ResponseError(c, err)
		} else if info == nil || !info.Finalized {
			api.ResponseError(c, errors.New("file not found on storage."))
		} else {
			//info.Tx.Size
			err = PipeStorageFile(c.Writer, info, ctx.Storage.Neurahive())
			if err != nil {
				api.ResponseError(c, err)
			}
		}
	} else {
		return false
	}
	return true
}

func PipeStorageFile(to io.Writer, info *node.FileInfo, storage *node.NeurahiveClient) error {
	chunk := uint64(0)
	size := info.Tx.Size
	totalChunk := size/file.DefaultChunkSize + 1
	for i := uint64(0); i < size; {
		end := chunk + file.DefaultSegmentMaxChunks
		if end > totalChunk {
			end = totalChunk
		}
		bytes, err := storage.DownloadSegment(info.Tx.DataMerkleRoot, chunk, end)
		if err != nil {
			return err
		}
		i += uint64(len(bytes))
		if i > size {
			bytes = bytes[0 : size%file.DefaultSegmentSize]
		}
		_, err = to.Write(bytes)
		if err != nil {
			return err
		}
		chunk = end
	}
	return nil
}
