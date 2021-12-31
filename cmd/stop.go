package cmd

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "stop msgservice",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := net.Dial("tcp", "127.0.0.1:"+viper.GetString("port"))
		if err != nil {
			return err
		}
		defer conn.Close()

		var req = struct {
			Type string `json:"type"`
		}{
			Type: "stop",
		}
		data, err := json.Marshal(req)
		if err != nil {
			return err
		}

		if _, err := conn.Write(data); err != nil {
			return err
		}

		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			return err
		}
		fmt.Println(string(buf[:n]))

		return nil
	},
}
