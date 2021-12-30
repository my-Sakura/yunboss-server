package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/my-Sakura/zinx/msgserver"
)

type Manager struct {
	server *msgserver.Server
}

func New(server *msgserver.Server) *Manager {
	return &Manager{
		server: server,
	}
}

func (m *Manager) Regist(r gin.IRouter) {
	r.POST("/sendmsg", m.pushMsg)
}

func (m *Manager) pushMsg(c *gin.Context) {
	var req struct {
		UID  string `json:"uid" binding:"required"`
		Body string `json:"body" binding:"required"`
		URL  string `json:"url" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest})
		log.Printf("Error bind request: %v\n", err)
		return
	}

	var user *msgserver.User
	if u, ok := m.server.UsersByUID.Load(req.UID); ok {
		user = u.(*msgserver.User)
	} else {
		c.JSON(http.StatusOK, gin.H{"status": 1, "msg": "client offline", "body": ""})
		log.Printf("Error client offline \n")
		return
	}

	request := &msgserver.ServerPushBody{
		Type: "serverpush",
		URL:  req.URL,
		Body: req.Body,
	}
	body, err := json.Marshal(request)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": 1, "msg": "internal error", "body": ""})
		log.Printf("Error marshal failed: %v\n", err)
		return
	}
	if _, err = user.Conn.Write(body); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": 1, "msg": "internal error", "body": ""})
		log.Printf("Error push failed: %v\n", err)
		return
	}

	receiveData := <-m.server.PushReturnCh
	fmt.Println(receiveData, "push")

	c.JSON(http.StatusOK, gin.H{"status": 0, "msg": receiveData.Msg, "body": receiveData.Body})
}
