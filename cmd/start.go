package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/my-Sakura/zinx/api"
	"github.com/my-Sakura/zinx/server"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
		var (
			config = &server.Config{}
			log    = logrus.New()
		)
		if err := viper.Unmarshal(config); err != nil {
			return err
		}
		server.LogPath = config.LogPath
		log.AddHook(&server.MyHook{})

		gin.SetMode(gin.DebugMode)
		var file *os.File
		defer file.Close()
		y, m, d := time.Now().Date()
		fileName := filepath.Join(config.LogPath, fmt.Sprintf("%d-%d-%d.log", y, int(m), d))
		_, err := os.Stat(fileName)
		if err != nil {
			if os.IsNotExist(err) {
				file, err = os.Create(fileName)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			file, err = os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY, os.ModePerm)
			if err != nil {
				return err
			}
		}
		fi, err := file.Stat()
		if err != nil {
			return err
		}
		if fi.Mode() != os.ModePerm {
			if err = file.Chmod(os.ModePerm); err != nil {
				return err
			}
		}
		gin.DefaultWriter = io.MultiWriter(os.Stdout, file)

		httpRouter := gin.Default()
		websocketRouter := gin.Default()

		httpRouter.Use(api.Cors())
		websocketRouter.Use(api.Cors())

		server := server.NewServer(config, log)
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
			log.WithFields(logrus.Fields{
				"err":  httpServer.ListenAndServe(),
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Info("httpServer start failed")
		}()

		return websocketServer.ListenAndServe()
	},
}
