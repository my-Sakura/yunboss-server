package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/my-Sakura/zinx/msgserver"
)

var (
	wsUpgrader = websocket.Upgrader{
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	ErrWebSocketClose = errors.New("websocket client close")
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
	r.GET("/sendmsg/ws", m.wsSendMsg)

	r.POST("/sendmsg", m.pushMsg)
}

func (m *Manager) wsSendMsg(c *gin.Context) {
	wsConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Error upgrade  websocket: %v\n", err)
		return
	}
	defer wsConn.Close()

	var (
		req struct {
			UID  string `json:"uid"`
			Body string `json:"body"`
			URL  string `json:"url"`
		}
		resp struct {
			Status int    `json:"status"`
			Msg    string `json:"msg"`
			Body   string `json:"body"`
		}
	)

	go func() {
		for {
			if err = wsConn.ReadJSON(&req); err != nil {
				resp.Status = 1
				resp.Msg = "please input json format data"

				if websocket.ErrCloseSent.Error() == err.Error() {
					log.Printf("%v\n", ErrWebSocketClose)
					return
				}
				log.Printf("Error ws read: %v\n", err)
				if err = wsConn.WriteJSON(resp); err != nil {
					if websocket.ErrCloseSent.Error() == err.Error() {
						log.Printf("%v\n", ErrWebSocketClose)
						return
					}
					log.Printf("Error ws write: %v\n", err)
					continue
				}
				continue
			}
			var user *msgserver.User
			if u, ok := m.server.UsersByUID.Load(req.UID); ok {
				user = u.(*msgserver.User)
			} else {
				resp.Status = 1
				resp.Msg = "client offline"
				resp.Body = ""
				if err = wsConn.WriteJSON(resp); err != nil {
					if websocket.ErrCloseSent.Error() == err.Error() {
						log.Printf("%v\n", ErrWebSocketClose)
						return
					}
					log.Printf("Error ws write: %v\n", err)
					continue
				}
				log.Printf("Error client offline \n")
				continue
			}

			request := &msgserver.ServerPushBody{
				Type: "serverpush",
				URL:  req.URL,
				Body: req.Body,
			}
			body, err := json.Marshal(request)
			if err != nil {
				resp.Status = 1
				resp.Msg = "internal error"
				resp.Body = ""
				if err = wsConn.WriteJSON(resp); err != nil {
					if websocket.ErrCloseSent.Error() == err.Error() {
						log.Printf("%v\n", ErrWebSocketClose)
						return
					}
					log.Printf("Error ws write: %v\n", err)
					continue
				}
				log.Printf("Error marshal failed: %v\n", err)
				continue
			}
			if _, err = user.Conn.Write(body); err != nil {
				resp.Status = 1
				resp.Msg = "internal error"
				resp.Body = ""
				if err = wsConn.WriteJSON(resp); err != nil {
					if websocket.ErrCloseSent.Error() == err.Error() {
						log.Printf("%v\n", ErrWebSocketClose)
						return
					}
					log.Printf("Error ws write: %v\n", err)
					continue
				}
				log.Printf("Error push failed: %v\n", err)
				continue
			}
		}
	}()

	for {
		receiveData := <-m.server.PushReturnCh
		fmt.Println(receiveData, "push")
		resp.Status = 0
		resp.Msg = receiveData.Msg
		resp.Body = receiveData.Body

		if err = wsConn.WriteJSON(resp); err != nil {
			if websocket.ErrCloseSent.Error() == err.Error() {
				log.Printf("%v\n", ErrWebSocketClose)
				return
			}
			log.Printf("Error ws write: %v\n", err)
			continue
		}
	}
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
