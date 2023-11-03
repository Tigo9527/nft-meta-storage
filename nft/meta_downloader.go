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
var callOpts = &bind.CallOpts{
	EpochNumber: types.EpochLatestState,
}

type ContractContext struct {
	base32addr    string
	totalSupply   big.Int
	idBulkCaller  *openzeppelin.ERC721EnumerableBulkCaller
	uriBulkCaller *openzeppelin.IERC721MetadataBulkCaller
}

func Setup() {
	url := "https://main.confluxrpc.org"
	client, err := sdk.NewClient(url)
	if err != nil {
		logrus.WithError(err).Fatal("can not create cfx client")
		return
	}
	cfxClient = client
	netId, _ := cfxClient.GetNetworkID()
	logrus.Info("network id ", netId)
}

func BuildERC721Enumerable(base32addr string) (*ContractContext, error) {
	address, err := cfxaddress.New(base32addr)
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

	metaBulkCaller, err := openzeppelin.NewIERC721MetadataBulkCaller(address, cfxClient)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call NewIERC721MetadataBulkCaller")
	}

	sup, err := erc721Caller.TotalSupply(callOpts)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to call TotalSupply")
	}

	ctx := &ContractContext{
		base32addr:    base32addr,
		totalSupply:   *sup,
		idBulkCaller:  idBulkCaller,
		uriBulkCaller: metaBulkCaller,
	}

	return ctx, nil
}

func GetTokenIds(ctx *ContractContext, offset int, limit int) ([]**big.Int, error) {
	if big.NewInt(int64(offset)).Cmp(&ctx.totalSupply) >= 0 {
		return nil, fmt.Errorf("offset exceeds total supply, %v > %v", offset, ctx.totalSupply)
	}

	bulkCaller := bulk.NewBulkCaller(cfxClient)
	var errArr []*error
	var ids []**big.Int
	index := offset
	for i := 0; i < limit; i++ {
		v, e := ctx.idBulkCaller.TokenByIndex(*bulkCaller, callOpts, big.NewInt(int64(index)))
		ids = append(ids, v)
		errArr = append(errArr, e)
		index++
	}
	errExec := bulkCaller.Execute()
	if errExec != nil {
		return nil, errors.WithMessage(errExec, "failed to call TokenByIndex")
	}

	var comboErr error
	index = offset
	for i := 0; i < limit; i++ {
		comboErr = errors.WithMessage(*errArr[i], fmt.Sprintf("has error at %v", index))
		index++
	}

	return ids, comboErr
}

func GetTokenURIs(ctx *ContractContext, ids []**big.Int) ([]*string, error) {
	bulkCaller := bulk.NewBulkCaller(cfxClient)
	var errArr []*error
	var uris []*string
	for _, id := range ids {
		v, e := ctx.uriBulkCaller.TokenURI(*bulkCaller, callOpts, *id)
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
	var comboErr error
	out, err := os.Create(dst)
	comboErr = errors.WithMessage(err, "failed to create file")
	defer out.Close()

	resp, err := http.Get(url)
	comboErr = errors.WithMessage(err, "failed to get file")
	defer resp.Body.Close()

	n, err := io.Copy(out, resp.Body)
	comboErr = errors.WithMessage(err, "failed to do io copy")

	return n, comboErr
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

	return result["image"].(string), nil
}
