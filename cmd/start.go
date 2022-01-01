package cmd

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/my-Sakura/zinx/api"
	"github.com/my-Sakura/zinx/server"
	"github.com/spf13/cobra"
)

const (
	_httpGroup      = ""
	_websocketGroup = ""
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start msgservice",
	RunE: func(cmd *cobra.Command, args []string) error {
		httpRouter := gin.Default()
		websocketRouter := gin.Default()

		httpRouter.Use(api.Cors())
		websocketRouter.Use(api.Cors())

		server := server.NewServer()
		go server.Start()

		h := api.NewHTTP(server)
		w := api.NewWebsocket(server)

		h.Regist(httpRouter.Group(_httpGroup))
		w.Regist(websocketRouter.Group(_websocketGroup))

		httpServer := &http.Server{
			Addr:         ":" + server.Config.Apiport,
			Handler:      httpRouter,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		websocketServer := &http.Server{
			Addr:         ":" + server.Config.Websocket,
			Handler:      websocketRouter,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		}

		go func() {
			log.Printf("httpServer start failed: %v\n", httpServer.ListenAndServe())
		}()

		return websocketServer.ListenAndServe()
	},
}
