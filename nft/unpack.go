package nft

import (
	"encoding/binary"
	"encoding/json"
	"github.com/Conflux-Chain/neurahive-client/file"
	"github.com/Conflux-Chain/neurahive-client/node"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
)

type ErrorGetFileInfo struct {
	cause error
}

func (e *ErrorGetFileInfo) Error() string {
	return "failed to get file info:" + e.cause.Error()
}

type ErrorFileNotFound struct {
}

func (e *ErrorFileNotFound) Error() string {
	return "file not found"
}

var ErrorFileEntryNotFound = errors.New("")

func GetFileByIndex(storage *node.Client, root string, name string, writer io.Writer) error {
	hash := common.HexToHash(root)
	info, err := storage.Neurahive().GetFileInfo(hash)
	if err != nil {
		return &ErrorGetFileInfo{err}
	}
	if info == nil {
		return &ErrorFileNotFound{}
	}
	marshal, _ := json.Marshal(info)
	logrus.Debug("file info ", string(marshal))
	totalChunks := ((info.Tx.Size - 1) / file.DefaultChunkSize) + 1 // math ceil
	readChunkAt := totalChunks - 1                                  // last chunk, 256 bytes
	lastChunkBytes := info.Tx.Size % file.DefaultChunkSize
	logrus.Debug("lastChunkBytes ", lastChunkBytes)
	var tailData []byte
	//info.Tx.Size
	// download last segment
	data, err := storage.Neurahive().DownloadSegment(hash, readChunkAt, readChunkAt+1)
	if err != nil {
		return errors.WithMessage(err, "failed to download file.")
	}
	if lastChunkBytes == 0 {
		lastChunkBytes = file.DefaultChunkSize
	} else {
		data = data[0:lastChunkBytes]
	}
	readChunkAt -= 1
	// need at least 4 bytes to recover the length of file info
	if lastChunkBytes < 4 {
		data2nd, err := storage.Neurahive().DownloadSegment(hash, readChunkAt, readChunkAt+1)
		if err != nil {
			return errors.WithMessage(err, "failed to download file!")
		}
		readChunkAt -= 1
		tailData = append(data2nd, data...)
	} else {
		tailData = data
	}

	//extract fileInfo length, that is, the last 4 bytes
	infoLen := binary.LittleEndian.Uint32(tailData[len(tailData)-4:])
	logrus.Debug("info length ", infoLen)
	for uint32(len(tailData)) < infoLen+4 {
		data3rd, err := storage.Neurahive().DownloadSegment(hash, readChunkAt, readChunkAt+1)
		if err != nil {
			return errors.WithMessage(err, "failed to download file~")
		}
		readChunkAt -= 1
		tailData = append(data3rd, tailData...)
	}

	infoStart := len(tailData) - int(infoLen) - 4
	var infoJson PackInfo
	infoBytes := tailData[infoStart : infoStart+int(infoLen)]
	logrus.Debug("info bytes ", string(infoBytes))
	if err := json.Unmarshal(infoBytes, &infoJson); err != nil {
		return errors.WithMessage(err, "failed to unmarshal info json")
	}

	fileInfo := infoJson.FindEntry(name)
	if fileInfo == nil {
		return ErrorFileEntryNotFound
	}
	if tmp, err := json.Marshal(fileInfo); err == nil {
		logrus.Debug("entry info ", name, " : ", string(tmp))
	}

	// download file from segment
	// TODO reuse downloaded tail data
	endChunk := fileInfo.EndChunk.Chunk
	if fileInfo.EndChunk.Byte == 0 {
		endChunk -= 1
	}
	batchChunk := int64(file.DefaultSegmentMaxChunks)
	saved := int64(0)
	for i := fileInfo.StartChunk.Chunk; i <= endChunk; i += batchChunk {
		end := i + batchChunk
		if end >= fileInfo.EndChunk.Chunk {
			end = fileInfo.EndChunk.Chunk + 1
		}
		logrus.Debug("downloading chunk [", i, " ", end, ")")
		data, err := storage.Neurahive().DownloadSegment(hash, uint64(i), uint64(end))
		logrus.Debug("got raw bytes ", len(data))
		if err != nil {
			return errors.WithMessage(err, "failed to download chunk")
		}
		if i == fileInfo.StartChunk.Chunk && fileInfo.StartChunk.Byte > 0 {
			data = data[fileInfo.StartChunk.Byte:]
			logrus.Debug("slice head ", fileInfo.StartChunk.Byte)
		}
		if end < fileInfo.EndChunk.Chunk {
			n, _ := writer.Write(data)
			saved += int64(n)
			logrus.Debug("written A ", n)
		} else {
			n, _ := writer.Write(data[0 : int64(len(data))-(file.DefaultChunkSize-fileInfo.EndChunk.Byte)])
			saved += int64(n)
			logrus.Debug("written B ", n)
		}
	}
	logrus.Debug("saved bytes ", saved)

	return nil
}
