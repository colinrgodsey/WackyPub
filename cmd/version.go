package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	Version    = "v0.1.0"
	ADKVersion = "v2.0.0"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information for WackyPub CLI",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("WackyPub CLI %s\n", Version)
		fmt.Printf("Google ADK Version: %s\n", ADKVersion)
		fmt.Printf("Go Version:         %s\n", runtime.Version())
		fmt.Printf("OS/Arch:            %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}
