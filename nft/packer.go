package nft

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/Conflux-Chain/neurahive-client/file"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
)

type FileEntry struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	StartChunk *ChunkPos `json:"start_chunk"`
	EndChunk   *ChunkPos `json:"end_chunk"`
}

type ChunkPos struct {
	Chunk int64 `json:"chunk"`
	Byte  int64 `json:"byte"`
}

type PackInfo struct {
	Dir     string       `json:"dir"`
	Entries []*FileEntry `json:"entries"`
}

var (
	ImageData = "image.data"
	MetaData  = "meta.data"
)

func (packInfo *PackInfo) FindEntry(name string) *FileEntry {
	for _, f := range packInfo.Entries {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func FilterFiles(dir string, pattern string) ([]string, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to read dir "+dir)
	}

	r, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.WithMessage(err, "invalid pattern"+pattern)
	}
	var matchedFiles []string
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if !r.MatchString(f.Name()) {
			continue
		}
		matchedFiles = append(matchedFiles, f.Name())
	}
	return matchedFiles, nil
}

func ConcatFiles(dir string, files []string, dst string) error {
	writer, err := os.Create(dst)
	if err != nil {
		return errors.WithMessage(err, "failed to open output file")
	}
	defer writer.Close()

	infoWriter, err := os.Create(fmt.Sprintf("%s.json", dst))
	if err != nil {
		return errors.WithMessage(err, "failed to create info file")
	}
	defer infoWriter.Close()

	packInfo := PackInfo{
		Dir:     dir,
		Entries: nil,
	}
	for _, f := range files {
		reader, err := os.Open(fmt.Sprintf("%s%c%s", dir, os.PathSeparator, f))
		if err != nil {
			return errors.WithMessage(err, "failed to open input file")
		}
		n, err := io.Copy(writer, reader)
		if err != nil {
			return errors.WithMessage(err, "failed to copy data")
		}
		packInfo.Entries = append(packInfo.Entries, &FileEntry{
			Name: f,
			Size: n,
		})
	}

	packInfo.SplitChunks()

	infoBytes, err := json.Marshal(packInfo)
	if err != nil {
		return errors.WithMessage(err, "failed to marshal pack info")
	}
	_, err = infoWriter.Write(infoBytes)
	if err != nil {
		return errors.WithMessage(err, "failed to write pack info")
	}

	//write info bytes to the end
	_, _ = writer.Write(infoBytes)
	//write length of info bytes to the end
	bs := make([]byte, 4)
	binary.LittleEndian.PutUint32(bs, uint32(len(infoBytes)))
	_, _ = writer.Write(bs)

	return nil
}

func (packInfo *PackInfo) SplitChunks() {
	chunk := int64(0)
	bytePos := int64(0)
	unitSize := int64(file.DefaultChunkSize)
	for _, entry := range packInfo.Entries {
		entry.StartChunk = &ChunkPos{Chunk: chunk, Byte: bytePos}

		segSpan := entry.Size / unitSize
		bytePos += entry.Size % unitSize
		if bytePos > unitSize {
			segSpan += 1
			bytePos -= unitSize
		}

		chunk += segSpan

		entry.EndChunk = &ChunkPos{Chunk: chunk, Byte: bytePos}
	}
}

func ReplaceImageInMeta(dir string, urlPrefix string) error {
	metaList, err := FilterFiles(dir, "\\d+\\.json")
	if err != nil {
		return errors.WithMessage(err, "failed to filter meta list")
	}

	imageList, err := FilterFiles(dir, "\\d+\\.image\\.")
	if err != nil {
		return errors.WithMessage(err, "failed to filter image list")
	}

	dataPath := fmt.Sprintf("%s%c%s", dir, os.PathSeparator, ImageData)
	wrappedFile, err := file.Open(dataPath)
	if err != nil {
		return errors.WithMessage(err, "failed to open "+dataPath)
	}
	tree, err := wrappedFile.MerkleTree()
	if err != nil {
		return errors.WithMessage(err, "failed to build merkle tree")
	}
	root := tree.Root()

	for _, metaFile := range metaList {
		metaFullPath := fmt.Sprintf("%s%c%s", dir, os.PathSeparator, metaFile)
		jsonObj, err := LoadJsonFile(metaFullPath)
		if err != nil {
			return errors.WithMessage(err, "failed to load meta json")
		}

		// do not overwrite existing field
		if jsonObj["_image"] == nil {
			jsonObj["_image"] = jsonObj["image"]
		}

		// eg. 123.meta contains 123.image.xxx.jpg
		matchedImage := matchImageList(metaFile, imageList)
		if matchedImage == "" {
			return fmt.Errorf("image not found for %s", metaFile)
		}
		jsonObj["image"] = fmt.Sprintf("%s/%s/%s", urlPrefix, root, matchedImage)

		bytes, _ := json.Marshal(jsonObj)
		err = ioutil.WriteFile(metaFullPath, bytes, 0666)
		if err != nil {
			return errors.WithMessage(err, "failed to save meta "+metaFullPath)
		}
	}

	return nil
}

func matchImageList(metaFile string, imageList []string) string {
	metaId := metaFile[0 : len(metaFile)-4] // eg. 123.json->123.
	metaId = metaId + "image."
	for _, f := range imageList {
		//logrus.Debug("matchImageList", f, " ", metaId)
		if strings.Index(f, metaId) == 0 {
			return f
		}
	}
	return ""
}

func PackMeta(dir string) error {
	return PackByPattern(dir, "^\\d+\\.json", MetaData)
}

func PackImage(dir string) error {
	return PackByPattern(dir, "^\\d+\\.image\\.", ImageData)
}

func PackByPattern(dir string, filterRegex string, binName string) error {
	files, err := FilterFiles(dir, filterRegex)
	if err != nil {
		return errors.WithMessage(err, "failed to filter mate files")
	}
	err = ConcatFiles(dir, files, dir+"/"+binName)
	if err != nil {
		return errors.WithMessage(err, "failed to pack mate files")
	}

	return nil
}
