/*
Copyright © 2023 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"github.com/Conflux-Chain/go-conflux-util/config"
	"nft.house/tools"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "nft.house",
	Short: "NFT.HOUSE",
	Long:  `NFT.HOUSE`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

var gormGenCmd = &cobra.Command{
	Use: "gormGen",
	Run: func(cmd *cobra.Command, args []string) {
		tools.GormGen()
	},
}

func init() {
	rootCmd.AddCommand(gormGenCmd)
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.nft.house.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	config.MustInit("")
}
