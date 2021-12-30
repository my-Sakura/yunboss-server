package main

import (
	"github.com/my-Sakura/zinx/cmd"
	// "github.com/my-Sakura/zinx/utils"
	// "github.com/my-Sakura/zinx/yunboss/controller"
)

const (
	_raftGroup  = ""
	_serverAddr = "0.0.0.0:5000"
)

func main() {
	cmd.Execute()
	// // gin.SetMode(gin.DebugMode)

	// engine := gin.Default()
	// engine.Use(utils.Cors())

	// c := controller.New()
	// if err := c.Regist(engine.Group(_raftGroup)); err != nil {
	// 	panic(err)
	// }

	// log.Printf("Error run server: %v\n", engine.Run(_serverAddr))
	// s := server.NewServer()
	// s.Start()
}
