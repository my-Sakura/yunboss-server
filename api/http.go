package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/my-Sakura/zinx/server"
	"github.com/sirupsen/logrus"
)

type HTTP struct {
	server *server.Server
}

func NewHTTP(server *server.Server) *HTTP {
	return &HTTP{
		server: server,
	}
}

func (h *HTTP) Regist(r gin.IRouter) {
	r.POST("/sendmsg", h.pushMsg)
}

func (h *HTTP) pushMsg(c *gin.Context) {
	var req struct {
		UID  string `json:"uid" binding:"required"`
		Body string `json:"body" binding:"required"`
		URL  string `json:"url" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest})
		h.server.Log.WithFields(logrus.Fields{
			"err":  err,
			"time": time.Now().Format("2006-01-02 15:04:05"),
		}).Errorln("Error bind request")
		return
	}

	var user *server.User
	if u, ok := h.server.UsersByUID.Load(req.UID); ok {
		user = u.(*server.User)
	} else {
		c.JSON(http.StatusOK, gin.H{"status": 1, "msg": "client offline", "body": ""})
		h.server.Log.WithFields(logrus.Fields{
			"time": time.Now().Format("2006-01-02 15:04:05"),
		}).Errorln("Error client offline")
		return
	}

	request := &server.ServerPushBody{
		Type: "serverpush",
		URL:  req.URL,
		Body: req.Body,
	}
	body, err := json.Marshal(request)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": 1, "msg": "internal error", "body": ""})
		h.server.Log.WithFields(logrus.Fields{
			"err":  err,
			"time": time.Now().Format("2006-01-02 15:04:05"),
		}).Errorln("Error marshal failed")
		return
	}
	if _, err = user.Conn.Write(body); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": 1, "msg": "internal error", "body": ""})
		h.server.Log.WithFields(logrus.Fields{
			"err":  err,
			"time": time.Now().Format("2006-01-02 15:04:05"),
		}).Errorln("Error push failed")
		return
	}

	receiveData := <-h.server.PushReturnCh

	c.JSON(http.StatusOK, gin.H{"status": 0, "msg": receiveData.Msg, "body": receiveData.Body})
}
