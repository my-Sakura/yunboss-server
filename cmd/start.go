package cmd

import (
	"github.com/gin-gonic/gin"
	"github.com/my-Sakura/zinx/api"
	"github.com/my-Sakura/zinx/msgserver"
	"github.com/spf13/cobra"
)

const (
	_apiGroup = ""
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start msgservice",
	RunE: func(cmd *cobra.Command, args []string) error {
		engine := gin.Default()
		engine.Use(api.Cors())

		server := msgserver.NewServer()
		m := api.New(server)
		m.Regist(engine.Group(_apiGroup))

		go server.Start()
		if err := engine.Run(":" + server.Config.Apiport); err != nil {
			return err
		}

		return nil
	},
}
