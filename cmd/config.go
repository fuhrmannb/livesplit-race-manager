package cmd

import (
	"fmt"
	"github.com/spf13/pflag"
	"os"
	"path"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configFileName = "livesplit-race-manager"
var envPrefix = "LIVESPLIT_RACE_MANAGER"

func InitConfig(cfgFile string, cmd *cobra.Command) func() {
	return func() {
		if cfgFile != "" {
			// Use config file from the flag
			viper.SetConfigFile(cfgFile)
		} else {
			// Find home directory
			home, err := os.UserHomeDir()
			if err != nil {
				// Search config in home directory
				viper.AddConfigPath(path.Join(home, fmt.Sprintf(".%s", configFileName)))
			}
			// Search config in etc directory
			viper.SetConfigType("yaml")
			viper.AddConfigPath("/etc/livesplit-race-manager")
			viper.SetConfigName(fmt.Sprintf("%s.yaml", configFileName))
		}

		viper.AutomaticEnv()
		viper.SetEnvPrefix(envPrefix)
		replacer := strings.NewReplacer("-", "_")
		viper.SetEnvKeyReplacer(replacer)

		if err := viper.ReadInConfig(); err == nil {
			log.Info().Str("config_file", viper.ConfigFileUsed()).Msg("using config file")
		}

		bindFlags(cmd)
	}
}

func ConfigVar(cmd *cobra.Command, cfgFile *string) {
	cmd.PersistentFlags().StringVar(cfgFile, "config", "", "config file location")
}

func bindFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		// Determine the naming convention of the flags when represented in the config file
		configName := f.Name

		// Apply the viper config value to the flag when the flag is not set and viper has a value
		if !f.Changed && viper.IsSet(configName) {
			val := viper.Get(configName)
			cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
		}
	})
}
