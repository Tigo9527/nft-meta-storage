package cmd

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"nft.house/nft"
	"strings"

	"github.com/spf13/cobra"
)

// downloadNftCmd represents the nft command
var downloadNftCmd = &cobra.Command{
	Use:   "download",
	Short: "",
	Long:  `download metas and images from a nft contract`,
	Run: func(cmd *cobra.Command, args []string) {
		rpc, _ := cmd.Flags().GetString("rpc")

		err := nft.Setup(rpc)
		if err != nil {
			logrus.WithError(err).Error("failed")
			return
		}

		contract, err := cmd.Flags().GetString("contract")
		if err != nil {
			logrus.WithError(err).Error("failed")
			return
		}

		ctx, err := nft.BuildERC721Enumerable(contract)
		if err != nil {
			logrus.WithError(err).Error("failed")
			return
		}

		ids, err := nft.GetTokenIds(ctx, 0, 10)
		if err != nil {
			logrus.WithError(err).Error("failed to get token ids")
		}

		uris, err := nft.GetTokenURIs(ctx, ids)
		if err != nil {
			logrus.WithError(err).Error("failed to get token uris")
		}

		logrus.Debug("token ids ")
		for index, ptr := range ids {
			if ptr == nil {
				continue
			}
			if *ptr == nil {
				continue
			}
			metaUrl := *(nft.FormatUri(uris[index], *ptr))
			logrus.Printf("%s %v \n", *ptr, metaUrl)

			dst := fmt.Sprintf("./download/%s.json", *ptr)
			_, err := nft.Download(metaUrl, dst)
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"tokenId": (*ptr).String(),
				}).Error("download file fail")
				continue
			}

			imgUrl, err := nft.ParseImage(dst)
			if err != nil {
				logrus.WithError(err).Error("failed to parse image")
				continue
			}

			filename := imgUrl[strings.LastIndex(imgUrl, "/")+1:]
			imgDst := fmt.Sprintf("./download/%s.image.%s", *ptr, filename)
			_, err = nft.Download(imgUrl, imgDst)
			if err != nil {
				logrus.WithError(err).Error("failed to download image")
			}
		}
		fmt.Println()
	},
}

var packImageCmd = &cobra.Command{
	Use: "packImage",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("pack image")
		logrus.WithError(nft.PackImage("./download")).Debug("pack image")
	},
}

var packMetaCmd = &cobra.Command{
	Use: "packMeta",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("pack meta")
		uri, _ := cmd.Flags().GetString("uri")
		if uri[len(uri)-1:] == "/" {
			// remove last slash
			uri = uri[0 : len(uri)-1]
		}
		err := nft.ReplaceImageInMeta("./download", uri, 0)
		if err != nil {
			logrus.Error("replace image err : ", err)
			return
		}
		logrus.WithError(nft.PackMeta("./download")).Debug("pack meta")
	},
}

func init() {
	rootCmd.AddCommand(downloadNftCmd)
	rootCmd.AddCommand(packImageCmd)
	rootCmd.AddCommand(packMetaCmd)

	packMetaCmd.Flags().String("uri", "", "gateway uri")
	_ = packMetaCmd.MarkPersistentFlagRequired("uri")

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	downloadNftCmd.PersistentFlags().String("contract", "", "base32 contract address")
	_ = downloadNftCmd.MarkPersistentFlagRequired("contract")
	downloadNftCmd.Flags().String("rpc", "", "blockchain rpc")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// downloadNftCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
