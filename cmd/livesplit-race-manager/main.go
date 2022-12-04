package main

import (
	"github.com/fuhrmannb/livesplit-race-manager/cmd"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	lsracemanager "github.com/fuhrmannb/livesplit-race-manager"
)

var (
	cfgFile            string
	logLevel           string
	logFormat          string
	listenAddress      string
	multiplexerAddress string

	rootCmd = &cobra.Command{
		Use:           "livesplit-race-manager",
		Short:         "LiveSplit Race Manager: Integration of gRPC multiplexer with multiple LiveSplit.Connect",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			err := cmd.SetupLog(logLevel, logFormat)
			if err != nil {
				return err
			}

			lsm, err := lsracemanager.NewLiveSplitManager(lsracemanager.LiveSplitManagerOpts{
				MultiplexerAddress: multiplexerAddress,
			})
			if err != nil {
				log.Error().Err(err).Msg("can't create LSM")
				return err
			}

			err = lsracemanager.NewAPI(lsracemanager.NewAPIOpts{
				LSM:     lsm,
				Address: listenAddress,
			})
			if err != nil {
				log.Error().Err(err).Msg("can't create api")
				return err
			}
			return err
		},
	}
)

func init() {
	cobra.OnInitialize(cmd.InitConfig(cfgFile, rootCmd))

	cmd.ConfigVar(rootCmd, &cfgFile)
	cmd.LogLevelVar(rootCmd, &logLevel)
	cmd.LogFormatVar(rootCmd, &logFormat)
	rootCmd.PersistentFlags().StringVarP(&listenAddress, "listen", "l", "localhost:8080",
		"Listen address for the HTTP API")
	rootCmd.PersistentFlags().StringVarP(&multiplexerAddress, "multiplexer-address", "m", "localhost:7592",
		"gRPC Multiplexer address where are connected the LiveSplit.Connect clients")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Err(err).Msg("client shutdown due to error")
		os.Exit(1)
	}
}
