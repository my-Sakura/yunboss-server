package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/my-Sakura/zinx/server"
	"github.com/sirupsen/logrus"
)

var (
	wsUpgrader = websocket.Upgrader{
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		HandshakeTimeout: 30 * time.Second,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	ErrWebSocketClose = errors.New("websocket client close")
)

type WebSocket struct {
	server *server.Server
}

func NewWebsocket(server *server.Server) *WebSocket {
	return &WebSocket{
		server: server,
	}
}

func (w *WebSocket) Regist(r gin.IRouter) {
	r.GET("/ws", w.ws)
}

func (w *WebSocket) ws(c *gin.Context) {
	wsConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		w.server.Log.WithFields(logrus.Fields{
			"err": err,
		}).Errorln("Error upgrade  websocket")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer wsConn.Close()
	defer func() {
		if err := recover(); err != nil {
			w.server.Log.Error(err)
		}
	}()

	go w.server.WSLogin(ctx, wsConn)
	go w.server.WSHeartBeat(ctx, wsConn)
	go w.server.WSReceiveMsg(ctx, wsConn)

	for {
		_, buf, err := wsConn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) ||
				websocket.ErrCloseSent.Error() == err.Error() ||
				websocket.IsCloseError(err, websocket.CloseGoingAway) {
				w.server.WSQuit(wsConn)
				w.server.Log.WithFields(logrus.Fields{
					"err":  ErrWebSocketClose,
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Errorln("wsConn readMessage error")
				return
			}
			if err = wsConn.WriteMessage(websocket.TextMessage, []byte("bad request")); err != nil {
				panic(err)
			}
			w.server.Log.WithFields(logrus.Fields{
				"err":  err,
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln("Error ws read")
			return
		}

		var readRequest struct {
			Type string `json:"type"`
		}
		if err = json.Unmarshal(buf, &readRequest); err != nil {
			if err = wsConn.WriteMessage(websocket.TextMessage, []byte("bad request")); err != nil {
				panic(err)
			}
			w.server.Log.WithFields(logrus.Fields{
				"err":  err,
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Error("Error ws read")
			w.server.Log.Errorln(err)
			continue
		}

		switch readRequest.Type {
		case "login":
			loginRequest := &server.ClientLoginBody{}
			if err = json.Unmarshal(buf, loginRequest); err != nil {
				panic(err)
			}
			w.server.WSLoginCh <- loginRequest

		case "heartbeat":
			var (
				heartBeatRequest = &server.ClientHeartBeatBody{}

				serverHeartBody = &server.ServerHeartBeatBody{
					Type:   "clientpush",
					Status: "",
					Msg:    "",
				}
			)

			if err = json.Unmarshal(buf, heartBeatRequest); err != nil {
				panic(err)
			}

			var user *server.User
			if u, ok := w.server.UsersByUID.Load(heartBeatRequest.UID); ok {
				user = u.(*server.User)
			} else {
				serverHeartBody.Status = "1"
				serverHeartBody.Msg = "no login"
				response, err := json.Marshal(serverHeartBody)
				if err != nil {
					panic(err)
				}

				if err = wsConn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				w.server.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Infoln("no login")
				return
			}

			if heartBeatRequest.Token != user.Token {
				serverHeartBody.Status = "1"
				serverHeartBody.Msg = "token error"
				response, err := json.Marshal(serverHeartBody)
				if err != nil {
					panic(err)
				}

				if err = wsConn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				return
			}

			w.server.WSHeartBeatCh <- heartBeatRequest

		case "clientpush":
			var (
				clientPush = &server.ClientPushBody{}

				clientReturnBody = &server.ClientReturnBody{
					Type:   "clientpush",
					Status: "",
					Msg:    "",
				}
			)
			if err = json.Unmarshal(buf, clientPush); err != nil {
				panic(err)
			}

			var user *server.User
			if u, ok := w.server.UsersByUID.Load(clientPush.UID); ok {
				user = u.(*server.User)
			} else {
				clientReturnBody.Status = "1"
				clientReturnBody.Msg = "no login"
				response, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}

				if err = wsConn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				w.server.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Infoln("no login")
				return
			}

			if clientPush.Token != user.Token {
				clientReturnBody.Status = "1"
				clientReturnBody.Msg = "token error"
				response, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}

				if err = wsConn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				return
			}
			w.server.WSReceiveCh <- clientPush

		default:
			if err = wsConn.WriteMessage(websocket.TextMessage, []byte("please input the right type")); err != nil {
				panic(err)
			}
			continue
		}
	}
}
