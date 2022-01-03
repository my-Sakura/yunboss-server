package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Server struct {
	Mux            *sync.Mutex
	Log            *logrus.Logger
	UsersByUID     *sync.Map
	UsersByConn    *sync.Map
	UsersByWSConn  *sync.Map
	Config         *Config
	Done           chan struct{}
	LoginCh        chan *ClientLoginBody
	HeartBeatCh    chan *ClientHeartBeatBody
	ReceiveCh      chan *ClientPushBody
	PushReturnCh   chan *ServerReturnBody
	WSLoginCh      chan *ClientLoginBody
	WSHeartBeatCh  chan *ClientHeartBeatBody
	WSReceiveCh    chan *ClientPushBody
	WSPushReturnCh chan *ServerReturnBody
}

func NewServer(config *Config, log *logrus.Logger) *Server {
	server := &Server{
		Log:            log,
		Config:         config,
		UsersByUID:     &sync.Map{},
		UsersByConn:    &sync.Map{},
		UsersByWSConn:  &sync.Map{},
		Done:           make(chan struct{}),
		LoginCh:        make(chan *ClientLoginBody),
		HeartBeatCh:    make(chan *ClientHeartBeatBody),
		ReceiveCh:      make(chan *ClientPushBody),
		PushReturnCh:   make(chan *ServerReturnBody),
		WSLoginCh:      make(chan *ClientLoginBody),
		WSHeartBeatCh:  make(chan *ClientHeartBeatBody),
		WSReceiveCh:    make(chan *ClientPushBody),
		WSPushReturnCh: make(chan *ServerReturnBody),
	}

	return server
}

func (s *Server) Start() {
	listener, err := net.Listen("tcp", ":"+s.Config.Port)
	if err != nil {
		s.Log.WithFields(logrus.Fields{
			"time": time.Now().Format("2006-01-02 15:04:05"),
		}).Fatalf("Error listen: %s\n", err.Error())
	}
	s.Log.Infof("time: %s      listen port: {socket: %s, websocket: %s, http: %s}\n", time.Now().Format("2006-01-02 15:04:05"), s.Config.Port, s.Config.Websocket, s.Config.Apiport)
	for {
		conn, err := listener.Accept()
		if err != nil {
			s.Log.WithFields(logrus.Fields{
				"err":  err.Error(),
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln("Error accept client")
			continue
		}

		if err = conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
			s.Log.WithFields(logrus.Fields{
				"err":  err.Error(),
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln("Error set readDeadline")
			continue
		}

		go s.Handler(conn)
	}
}

func (s *Server) Handler(conn net.Conn) {
	defer func() {
		if u, ok := s.UsersByConn.Load(conn); ok {
			user := u.(*User)
			s.UsersByUID.Delete(user.Conn)
		}

		s.UsersByWSConn.Delete(conn)
		conn.Close()
	}()

	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"err":  err,
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.Login(ctx, conn)
	go s.HeartBeat(ctx, conn)
	go s.ReceiveMsg(ctx, conn)

	for {
		var buf = make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			if err == io.EOF {
				s.Quit(conn)
				return
			}
			panic(err)
		}
		var readRequest struct {
			Type string `json:"type"`
		}
		if err = json.Unmarshal(buf[:n], &readRequest); err != nil {
			panic(err)
		}

		switch readRequest.Type {
		case "login":
			loginRequest := &ClientLoginBody{}
			if err = json.Unmarshal(buf[:n], loginRequest); err != nil {
				panic(err)
			}
			s.Log.Infof("time: %s      login body: {type: %s, uid: %s, body: %s}\n", time.Now().Format("2006-01-02 15:04:05"),
				loginRequest.Type, loginRequest.Uid, loginRequest.Body)
			s.LoginCh <- loginRequest

		case "heartbeat":
			var (
				heartBeatRequest = &ClientHeartBeatBody{}

				serverHeartBody = &ServerHeartBeatBody{
					Type:   "clientpush",
					Status: "",
					Msg:    "",
				}
			)
			if err = json.Unmarshal(buf[:n], heartBeatRequest); err != nil {
				panic(err)
			}
			s.Log.Infof("time: %s      heartbeat body: {type: %s, uid: %s, body: %s, token: %s}\n",
				time.Now().Format("2006-01-02 15:04:05"), heartBeatRequest.Type,
				heartBeatRequest.UID, heartBeatRequest.Body, heartBeatRequest.Token)
			var user *User
			if u, ok := s.UsersByUID.Load(heartBeatRequest.UID); ok {
				user = u.(*User)
			} else {
				serverHeartBody.Status = "1"
				serverHeartBody.Msg = "no login"
				response, err := json.Marshal(serverHeartBody)
				if err != nil {
					panic(err)
				}

				if _, err = conn.Write(response); err != nil {
					panic(err)
				}
				s.Log.WithFields(logrus.Fields{
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

				if _, err = conn.Write(response); err != nil {
					panic(err)
				}
				return
			}
			s.HeartBeatCh <- heartBeatRequest

		case "clientpush":
			var (
				clientPush       = &ClientPushBody{}
				clientReturnBody = &ClientReturnBody{
					Type:   "clientpush",
					Status: "",
					Msg:    "",
				}
			)
			if err = json.Unmarshal(buf[:n], clientPush); err != nil {
				panic(err)
			}

			s.Log.Infof("time: %s      client push body: {type: %s, uid: %s, body: %s, token: %s}\n",
				time.Now().Format("2006-01-02 15:04:05"), clientPush.Type,
				clientPush.UID, clientPush.Body, clientPush.Token)
			var user *User
			if u, ok := s.UsersByUID.Load(clientPush.UID); ok {
				user = u.(*User)
			} else {
				clientReturnBody.Status = "1"
				clientReturnBody.Msg = "no login"
				response, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}

				if _, err = conn.Write(response); err != nil {
					panic(err)
				}
				s.Log.WithFields(logrus.Fields{
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

				if _, err = conn.Write(response); err != nil {
					panic(err)
				}
				return
			}
			s.ReceiveCh <- clientPush

		case "serverpush":
			serverPush := &ServerReturnBody{}
			if err = json.Unmarshal(buf[:n], serverPush); err != nil {
				panic(err)
			}
			s.Log.Infof("time: %s      server push body: {type: %s, msg: %s, body: %s, status: %d}\n",
				time.Now().Format("2006-01-02 15:04:05"), serverPush.Type,
				serverPush.Msg, serverPush.Body, serverPush.Status)
			s.PushReturnCh <- serverPush

		case "stop":
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Infoln("Precess exit use stop command")
			if _, err := conn.Write([]byte("msgserver exit")); err != nil {
				panic(err)
			}
			os.Exit(0)

		case "reload":
			config := &Config{}
			if err := viper.Unmarshal(config); err != nil {
				panic(err)
			}
			s.Config = config
			if _, err := conn.Write([]byte("reload succeed")); err != nil {
				panic(err)
			}

			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Infoln("config reload succeed")
			return

		case "status":
			if _, err := conn.Write([]byte("msgserver running")); err != nil {
				panic(err)
			}
			return

		default:
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Infoln("debug")
		}
	}
}

func (s *Server) Login(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()

	url := "http://" + s.Config.Yunboss + "/oservice/client/login"
	select {
	case receiveData := <-s.LoginCh:
		if u, ok := s.UsersByUID.Load(receiveData.Uid); ok {
			user := u.(*User)
			loginResp := &ServerLoginBody{
				UID:    receiveData.Uid,
				Status: http.StatusConflict,
				Msg:    "uid repeated",
				Token:  user.Token,
			}
			data, err := json.Marshal(loginResp)
			if err != nil {
				s.Log.WithFields(logrus.Fields{
					"err": err.Error(),
				}).Errorln("marshal error")
			}
			if _, err = conn.Write(data); err != nil {
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).WithFields(logrus.Fields{
					"err": err.Error(),
				}).Errorln("write error")
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).WithFields(logrus.Fields{
					"err": err.Error(),
				}).Errorln("marshal error")
			}
		}
		var req = struct {
			Ip   string `json:"ip"`
			Uid  string `json:"uid"`
			Body string `json:"body"`
		}{
			Ip:   s.Config.Ip,
			Uid:  receiveData.Uid,
			Body: receiveData.Body,
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			panic(err)
		}
		reader := bytes.NewReader(reqBody)
		request, err := http.NewRequest("POST", url, reader)
		if err != nil {
			panic(err)
		}
		request.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(request)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()

		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		loginResponse := &ServerLoginBody{}
		if err = json.Unmarshal(respBody, loginResponse); err != nil {
			panic(err)
		}
		if loginResponse.Status != 0 {
			loginResponse.Status = http.StatusInternalServerError
			loginResponse.Msg = "login failed"
		} else {
			loginResponse.Status = http.StatusOK
			loginResponse.Msg = "login succeed"
		}

		loginResponse.Type = "login"
		response, err := json.Marshal(&loginResponse)
		if err != nil {
			panic(err)
		}
		_, err = conn.Write(response)
		if err != nil {
			panic(err)
		}

		user := NewUser(receiveData.Uid, WithConn(conn), WithToken(loginResponse.Token))
		s.UsersByUID.Store(user.UID, user)
		s.UsersByConn.Store(conn, user)

	case <-ctx.Done():
		return
	}
}

func (s *Server) WSLogin(ctx context.Context, conn *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()

	url := "http://" + s.Config.Yunboss + "/oservice/client/login"
	select {
	case receiveData := <-s.WSLoginCh:
		if u, ok := s.UsersByUID.Load(receiveData.Uid); ok {
			user := u.(*User)
			loginResp := &ServerLoginBody{
				UID:    receiveData.Uid,
				Status: http.StatusConflict,
				Msg:    "uid repeated",
				Token:  user.Token,
			}
			data, err := json.Marshal(loginResp)
			if err != nil {
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).WithFields(logrus.Fields{
					"err": err.Error(),
				}).Errorln("marshal error")
				return
			}
			if err = conn.WriteMessage(websocket.TextMessage, data); err != nil {
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).WithFields(logrus.Fields{
					"err": err.Error(),
				}).Errorln("write error")
				return
			}
		}
		var req = struct {
			Ip   string `json:"ip"`
			Uid  string `json:"uid"`
			Body string `json:"body"`
		}{
			Ip:   s.Config.Ip,
			Uid:  receiveData.Uid,
			Body: receiveData.Body,
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			panic(err)
		}
		reader := bytes.NewReader(reqBody)
		request, err := http.NewRequest("POST", url, reader)
		if err != nil {
			panic(err)
		}
		request.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(request)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()

		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		loginResponse := &ServerLoginBody{}
		if err = json.Unmarshal(respBody, loginResponse); err != nil {
			panic(err)
		}
		if loginResponse.Status != 0 {
			loginResponse.Status = http.StatusInternalServerError
			loginResponse.Msg = "login failed"
		} else {
			loginResponse.Status = http.StatusOK
			loginResponse.Msg = "login succeed"
		}

		loginResponse.Type = "login"
		response, err := json.Marshal(loginResponse)
		if err != nil {
			panic(err)
		}
		err = conn.WriteMessage(websocket.TextMessage, response)
		if err != nil {
			panic(err)
		}

		user := NewUser(receiveData.Uid, WithWSConn(conn), WithToken(loginResponse.Token))
		s.UsersByUID.Store(user.UID, user)
		s.UsersByWSConn.Store(conn, user)

	case <-ctx.Done():
		return
	}
}

func (s *Server) HeartBeat(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()
	url := "http://" + s.Config.Yunboss + "/oservice/client/heartbeat"

	for {
		select {
		case receiveData := <-s.HeartBeatCh:
			heartBeatResponse := &ServerHeartBeatBody{
				Type:   "heartbeat",
				Status: "",
				Msg:    "",
			}
			var user *User
			if u, ok := s.UsersByUID.Load(receiveData.UID); ok {
				user = u.(*User)
			} else {
				heartBeatResponse.Status = "1"
				heartBeatResponse.Msg = "no login"
				response, err := json.Marshal(heartBeatResponse)
				if err != nil {
					panic(err)
				}

				if _, err = conn.Write(response); err != nil {
					panic(err)
				}
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Infoln("no login")
				continue
			}

			body, err := json.Marshal(receiveData.Body)
			if err != nil {
				panic(err)
			}
			var (
				req = struct {
					UID   string `json:"uid"`
					IP    string `json:"ip"`
					Token string `json:"token"`
					Body  string `json:"body"`
				}{
					UID:   receiveData.UID,
					IP:    s.Config.Ip,
					Token: user.Token,
					Body:  string(body),
				}
			)

			reqBody, err := json.Marshal(req)
			if err != nil {
				panic(err)
			}
			reader := bytes.NewReader(reqBody)
			request, err := http.NewRequest("POST", url, reader)
			if err != nil {
				panic(err)
			}
			request.Header.Set("Content-Type", "application/json")
			client := http.Client{
				Timeout: time.Second * 3,
			}
			resp, err := client.Do(request)
			if err != nil {
				if strings.Contains(err.Error(), "Client.Timeout exceeded") {
					s.Log.WithFields(logrus.Fields{
						"time": time.Now().Format("2006-01-02 15:04:05"),
					}).Errorln("HTTP Post timeout")
				}
				heartBeatResponse.Status = "1"
				heartBeatResponse.Msg = "overtime"
				response, err := json.Marshal(heartBeatResponse)
				if err != nil {
					panic(err)
				}
				if _, err = user.Conn.Write(response); err != nil {
					panic(err)
				}
				return
			}
			defer resp.Body.Close()

			heartBeatResponse.Status = "0"
			heartBeatResponse.Msg = "heartbeat succeed"
			response, err := json.Marshal(heartBeatResponse)
			if err != nil {
				panic(err)
			}

			if _, err = user.Conn.Write(response); err != nil {
				panic(err)
			}

		case <-ctx.Done():
			return

		}
	}
}

func (s *Server) WSHeartBeat(ctx context.Context, conn *websocket.Conn) {
	url := "http://" + s.Config.Yunboss + "/oservice/client/heartbeat"

	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()

	for {
		select {
		case receiveData := <-s.WSHeartBeatCh:
			heartBeatResponse := &ServerHeartBeatBody{
				Type:   "heartbeat",
				Status: "",
				Msg:    "",
			}
			var user *User
			if u, ok := s.UsersByUID.Load(receiveData.UID); ok {
				user = u.(*User)
			} else {
				heartBeatResponse.Status = "1"
				heartBeatResponse.Msg = "no login"
				response, err := json.Marshal(heartBeatResponse)
				if err != nil {
					panic(err)
				}

				if err = conn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Errorln("bad request")
				continue
			}

			body, err := json.Marshal(receiveData.Body)
			if err != nil {
				panic(err)
			}
			var (
				req = struct {
					UID   string `json:"uid"`
					IP    string `json:"ip"`
					Token string `json:"token"`
					Body  string `json:"body"`
				}{
					UID:   receiveData.UID,
					IP:    s.Config.Ip,
					Token: user.Token,
					Body:  string(body),
				}
			)
			reqBody, err := json.Marshal(req)
			if err != nil {
				panic(err)
			}
			reader := bytes.NewReader(reqBody)
			request, err := http.NewRequest("POST", url, reader)
			if err != nil {
				panic(err)
			}
			request.Header.Set("Content-Type", "application/json")
			client := http.Client{
				Timeout: time.Second * 3,
			}
			resp, err := client.Do(request)
			if err != nil {
				if strings.Contains(err.Error(), "Client.Timeout exceeded") {
					s.Log.WithFields(logrus.Fields{
						"time": time.Now().Format("2006-01-02 15:04:05"),
					}).Errorln("HTTP Post timeout")
				}
				heartBeatResponse.Status = "1"
				heartBeatResponse.Msg = "overtime"
				response, err := json.Marshal(heartBeatResponse)
				if err != nil {
					panic(err)
				}
				if err = user.WSConn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				return
			}
			defer resp.Body.Close()

			heartBeatResponse.Status = "0"
			heartBeatResponse.Msg = "heartbeat succeed"
			response, err := json.Marshal(heartBeatResponse)
			if err != nil {
				panic(err)
			}

			if err = user.WSConn.WriteMessage(websocket.TextMessage, response); err != nil {
				panic(err)
			}

		case <-ctx.Done():
			return

		}
	}
}

func (s *Server) PushMsg(uid, msg, url string) error {
	var user *User
	if u, ok := s.UsersByUID.Load(uid); ok {
		user = u.(*User)
	} else {
		return errors.New("load user failed")
	}

	req, err := json.Marshal(struct {
		UID  string `json:"uid"`
		Body string `json:"body"`
		URL  string `json:"url"`
	}{
		UID:  uid,
		Body: msg,
		URL:  url,
	})
	if err != nil {
		return err
	}

	if _, err = user.Conn.Write(req); err != nil {
		return err
	}

	return nil
}

func (s *Server) ReceiveMsg(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()
	url := "http://" + s.Config.Yunboss + "/oservice/client/msg"

	for {
		select {
		case receiveData := <-s.ReceiveCh:
			var (
				clientReturnBody = &ClientReturnBody{
					Type:   "clientpush",
					Status: "",
					Msg:    "",
				}
			)
			var user *User
			if u, ok := s.UsersByUID.Load(receiveData.UID); ok {
				user = u.(*User)
			} else {
				clientReturnBody.Status = "1"
				clientReturnBody.Msg = "no login"
				r, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}
				if _, err = user.Conn.Write(r); err != nil {
					panic(err)
				}
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Infoln("no login")
				continue
			}

			req, err := json.Marshal(struct {
				UID   string `json:"uid"`
				Body  string `json:"body"`
				Token string `json:"token"`
				Ip    string `json:"ip"`
			}{
				UID:   receiveData.UID,
				Body:  receiveData.Body,
				Token: receiveData.Token,
				Ip:    s.Config.Ip,
			})
			if err != nil {
				panic(err)
			}
			reader := bytes.NewReader(req)
			request, err := http.NewRequest("POST", url, reader)
			if err != nil {
				panic(err)
			}
			request.Header.Set("Content-Type", "application/json")
			client := http.Client{
				Timeout: time.Second * 3,
			}
			resp, err := client.Do(request)
			if err != nil {
				clientReturnBody.Status = "1"
				if strings.Contains(err.Error(), "Client.Timeout exceeded") {
					clientReturnBody.Msg = "overtime"
					s.Log.WithFields(logrus.Fields{
						"err":  err.Error(),
						"time": time.Now().Format("2006-01-02 15:04:05"),
					}).Errorln("HTTP Post timeout")
				} else {
					clientReturnBody.Msg = "request yunboss error"
					s.Log.WithFields(logrus.Fields{
						"err":  err.Error(),
						"time": time.Now().Format("2006-01-02 15:04:05"),
					}).Errorln("request yunboss error")
				}
				response, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}
				if _, err = user.Conn.Write(response); err != nil {
					panic(err)
				}
				continue
			}
			defer resp.Body.Close()

			clientReturnBody.Status = "0"
			clientReturnBody.Msg = "push succeed"
			r, err := json.Marshal(clientReturnBody)
			if err != nil {
				panic(err)
			}
			if _, err = user.Conn.Write(r); err != nil {
				panic(err)
			}

		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) WSReceiveMsg(ctx context.Context, conn *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()

	url := "http://" + s.Config.Yunboss + "/oservice/client/msg"
	for {
		select {
		case receiveData := <-s.WSReceiveCh:
			var (
				clientReturnBody = &ClientReturnBody{
					Type:   "clientpush",
					Status: "",
					Msg:    "",
				}
			)
			if _, ok := s.UsersByUID.Load(receiveData.UID); !ok {
				clientReturnBody.Status = "1"
				clientReturnBody.Msg = "no login"
				response, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}

				if err = conn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				s.Log.WithFields(logrus.Fields{
					"time": time.Now().Format("2006-01-02 15:04:05"),
				}).Infoln("no login")
				continue
			}

			req, err := json.Marshal(struct {
				UID   string `json:"uid"`
				Body  string `json:"body"`
				Token string `json:"token"`
				Ip    string `json:"ip"`
			}{
				UID:   receiveData.UID,
				Body:  receiveData.Body,
				Token: receiveData.Token,
				Ip:    s.Config.Ip,
			})
			if err != nil {
				panic(err)
			}
			reader := bytes.NewReader(req)
			request, err := http.NewRequest("POST", url, reader)
			if err != nil {
				panic(err)
			}
			request.Header.Set("Content-Type", "application/json")

			client := http.Client{
				Timeout: time.Second * 3,
			}
			resp, err := client.Do(request)
			if err != nil {
				clientReturnBody.Status = "1"
				if strings.Contains(err.Error(), "Client.Timeout exceeded") {
					clientReturnBody.Msg = "overtime"
					s.Log.WithFields(logrus.Fields{
						"time": time.Now().Format("2006-01-02 15:04:05"),
					}).Info("HTTP Post timeout")
				} else {
					clientReturnBody.Msg = "request yunboss error"
					s.Log.WithFields(logrus.Fields{
						"time": time.Now().Format("2006-01-02 15:04:05"),
					}).Info("request yunboss error")
				}
				response, err := json.Marshal(clientReturnBody)
				if err != nil {
					panic(err)
				}
				if err = conn.WriteMessage(websocket.TextMessage, response); err != nil {
					panic(err)
				}
				continue
			}
			defer resp.Body.Close()

			clientReturnBody.Msg = "push succeed"
			clientReturnBody.Status = "0"
			r, err := json.Marshal(clientReturnBody)
			if err != nil {
				panic(err)
			}
			if err = conn.WriteMessage(websocket.TextMessage, r); err != nil {
				panic(err)
			}

		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) Quit(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Errorln(err)
		}
	}()

	url := "http://" + s.Config.Yunboss + "/oservice/client/quit"

	var user *User
	if u, ok := s.UsersByConn.Load(conn); ok {
		user = u.(*User)
	} else {
		panic("load user failed")
	}
	req, err := json.Marshal(struct {
		UID   string `json:"uid"`
		Token string `json:"token"`
		IP    string `json:"ip"`
	}{
		UID:   user.UID,
		IP:    s.Config.Ip,
		Token: user.Token,
	})
	if err != nil {
		panic(err)
	}
	s.UsersByConn.Delete(conn)
	s.UsersByUID.Delete(user.UID)
	reader := bytes.NewReader(req)
	request, err := http.NewRequest("POST", url, reader)
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	s.Log.WithFields(logrus.Fields{
		"time": time.Now().Format("2006-01-02 15:04:05"),
	}).Infoln(string(respBody))
	s.Log.WithFields(logrus.Fields{
		"uid":  user.UID,
		"time": time.Now().Format("2006-01-02 15:04:05"),
	}).Infoln("client exit")
}

func (s *Server) WSQuit(conn *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			s.Log.WithFields(logrus.Fields{
				"time": time.Now().Format("2006-01-02 15:04:05"),
			}).Info(err)
		}
	}()

	url := "http://" + s.Config.Yunboss + "/oservice/client/quit"

	var user *User
	if u, ok := s.UsersByWSConn.Load(conn); ok {
		user = u.(*User)
	} else {
		panic("load user failed")
	}
	req, err := json.Marshal(struct {
		UID   string `json:"uid"`
		Token string `json:"token"`
		IP    string `json:"ip"`
	}{
		UID:   user.UID,
		IP:    s.Config.Ip,
		Token: user.Token,
	})
	if err != nil {
		panic(err)
	}
	reader := bytes.NewReader(req)
	request, err := http.NewRequest("POST", url, reader)
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	s.Log.WithFields(logrus.Fields{
		"time": time.Now().Format("2006-01-02 15:04:05"),
	}).Infoln(string(respBody))
	s.Log.WithFields(logrus.Fields{
		"uid":  user.UID,
		"time": time.Now().Format("2006-01-02 15:04:05"),
	}).Infoln("client exit")
}
