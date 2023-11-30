package nft

import (
	"encoding/json"
	"fmt"
	sdk "github.com/Conflux-Chain/go-conflux-sdk"
	"github.com/Conflux-Chain/go-conflux-sdk/bind"
	"github.com/Conflux-Chain/go-conflux-sdk/cfxclient/bulk"
	"github.com/Conflux-Chain/go-conflux-sdk/types"
	"github.com/Conflux-Chain/go-conflux-sdk/types/cfxaddress"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	openzeppelin "nft.house/nft/contracts"
	"os"
	"strings"
)

var cfxClient *sdk.Client
var netId uint32
var CallOpts = &bind.CallOpts{
	EpochNumber: types.EpochLatestState,
}

type ContractContext struct {
	base32addr    string
	TotalSupply   big.Int
	IdBulkCaller  *openzeppelin.ERC721EnumerableBulkCaller
	uriBulkCaller *openzeppelin.IERC721MetadataBulkCaller
	Name          string
	Caller        *openzeppelin.ERC721EnumerableCaller
	Caller1155    *openzeppelin.IERC1155MetadataURICaller
}

func Setup(rpc string) error {
	url := "https://main.confluxrpc.org"
	if len(rpc) > 0 {
		url = rpc
	}
	client, err := sdk.NewClient(url)
	if err != nil {
		logrus.WithError(err).Error("can not create cfx client")
		return err
	}
	cfxClient = client
	netId, err := cfxClient.GetNetworkID()
	logrus.WithFields(logrus.Fields{"rpc": rpc, "error": err}).Info("network id ", netId)
	return nil
}

func BuildERC721Enumerable(base32addr string) (*ContractContext, error) {
	address, err := cfxaddress.New(base32addr, netId)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to create cfx address from "+base32addr)
	}

	idBulkCaller, err := openzeppelin.NewERC721EnumerableBulkCaller(address, cfxClient)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call NewERC721EnumerableCaller")
	}

	erc721Caller, err := openzeppelin.NewERC721EnumerableCaller(address, cfxClient)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call NewERC721EnumerableCaller")
	}

	erc1155UriCaller, err := openzeppelin.NewIERC1155MetadataURICaller(address, cfxClient)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call NewIERC1155MetadataURICaller")
	}

	metaBulkCaller, err := openzeppelin.NewIERC721MetadataBulkCaller(address, cfxClient)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call NewIERC721MetadataBulkCaller")
	}

	sup, err := erc721Caller.TotalSupply(CallOpts)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call TotalSupply")
	}

	name, err := erc721Caller.Name(CallOpts)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to get name")
	}

	ctx := &ContractContext{
		base32addr:    base32addr,
		TotalSupply:   *sup,
		IdBulkCaller:  idBulkCaller,
		uriBulkCaller: metaBulkCaller,
		Name:          name,
		Caller:        erc721Caller,
		Caller1155:    erc1155UriCaller,
	}
	logrus.Info("name is ", name)
	return ctx, nil
}

func GetTokenIds(ctx *ContractContext, offset int, limit int) ([]**big.Int, error) {
	intTotal := int(ctx.TotalSupply.Int64())
	if offset >= intTotal {
		return nil, fmt.Errorf("offset exceeds total supply, %v > %v", offset, ctx.TotalSupply)
	}

	bulkCaller := bulk.NewBulkCaller(cfxClient)
	var errArr []*error
	var ids []**big.Int
	index := offset
	for i := 0; i < limit; i++ {
		if index >= intTotal {
			break
		}
		v, e := ctx.IdBulkCaller.TokenByIndex(*bulkCaller, CallOpts, big.NewInt(int64(index)))
		ids = append(ids, v)
		errArr = append(errArr, e)
		index++
	}
	errExec := bulkCaller.Execute()
	if errExec != nil {
		return nil, errors.WithMessage(errExec, "failed to call TokenByIndex")
	}

	var comboErr = ""
	for i := range errArr {
		if *errArr[i] != nil {
			comboErr += (*errArr[i]).Error() + ";"
		}
	}
	if len(comboErr) > 0 {
		return nil, errors.New(comboErr)
	}
	return ids, nil
}

func GetTokenURIs(ctx *ContractContext, ids []**big.Int) ([]*string, error) {
	bulkCaller := bulk.NewBulkCaller(cfxClient)
	var errArr []*error
	var uris []*string
	for _, id := range ids {
		v, e := ctx.uriBulkCaller.TokenURI(*bulkCaller, CallOpts, *id)
		uris = append(uris, v)
		errArr = append(errArr, e)
	}
	errExec := bulkCaller.Execute()
	if errExec != nil {
		return nil, errors.WithMessage(errExec, "failed to call TokenURI")
	}

	var comboErr error
	for index, e := range errArr {
		comboErr = errors.WithMessage(*e, fmt.Sprintf("has error at %s", *(ids[index])))
	}
	return uris, comboErr
}

func FormatUri(uri *string, tokenId *big.Int) *string {
	if uri == nil {
		return nil
	}
	str := *uri
	if !strings.Contains(str, "{id}") {
		return uri
	}
	strId := fmt.Sprintf("%064x", tokenId)
	str = strings.Replace(str, "{id}", strId, 1)
	return &str
}

func Download(url string, dst string) (int64, error) {
	out, err1 := os.Create(dst)
	err1 = errors.WithMessage(err1, "failed to create file")
	if err1 != nil {
		return 0, err1
	}
	defer out.Close()

	resp, err2 := http.Get(url)
	err2 = errors.WithMessage(err2, "failed to get file")
	if err2 != nil {
		return 0, err2
	}
	defer resp.Body.Close()

	n, err3 := io.Copy(out, resp.Body)
	err3 = errors.WithMessage(err3, "failed to do io copy")
	if err3 != nil {
		return 0, err3
	}

	return n, nil
}

func LoadJsonFile(filePath string) (map[string]interface{}, error) {
	bytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to read file "+filePath)
	}

	var result map[string]interface{}
	err = json.Unmarshal(bytes, &result)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to unmarshal file "+filePath)
	}

	return result, nil
}

func ParseImage(filePath string) (string, error) {
	result, err := LoadJsonFile(filePath)
	if err != nil {
		return "", err
	}

	imageMaybe, ok := result["image"]
	if !ok {
		return "", errors.Errorf("image not found in meta")
	}
	imageStr, ok := imageMaybe.(string)
	if !ok {
		return "", errors.Errorf("image is not a string: %v", imageMaybe)
	}
	return imageStr, nil
}
