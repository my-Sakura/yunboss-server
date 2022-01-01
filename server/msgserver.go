package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

type Server struct {
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

type HttpHeartBeatBody []struct {
	IP   string `json:"ip"`
	UID  string `json:"uid"`
	Body struct {
		Process struct {
			Nginx int `json:"nginx"`
			Php   int `json:"php"`
			Mysql int `json:"mysql"`
		} `json:"process"`
		HTTP struct {
			Disk int `json:"disk"`
		} `json:"http"`
		Shell struct {
			Network string `json:"network"`
		} `json:"shell"`
	} `json:"body"`
}

func NewServer() *Server {
	config := &Config{}
	if err := viper.Unmarshal(config); err != nil {
		panic(err)
	}

	server := &Server{
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
		log.Fatalf("Error listen: %s", err.Error())
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accept client: %s", err.Error())
			continue
		}

		if err = conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
			log.Println(err)
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
			log.Println(err)
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
				log.Println("no login")
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
				log.Println("no login")
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
			s.PushReturnCh <- serverPush

		case "stop":
			fmt.Println("process exit")
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

			fmt.Println("config reload succeed")
			return

		case "status":
			if _, err := conn.Write([]byte("msgserver running")); err != nil {
				panic(err)
			}
			return

		default:
			fmt.Println("debug")
		}
	}
}

func (s *Server) Login(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
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
				fmt.Println("marshal error:", err)
			}
			if _, err = conn.Write(data); err != nil {
				fmt.Println("write error:", err)
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
			log.Println(err)
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
				fmt.Println("marshal error:", err)
				return
			}
			if err = conn.WriteMessage(websocket.TextMessage, data); err != nil {
				fmt.Println("write error:", err)
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

		fmt.Println(loginResponse, "loginResponse")
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
			log.Println(err)
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
				log.Println("no login")
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
					fmt.Println("HTTP Post timeout")
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
			log.Println(err)
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
			fmt.Println("headrrqrqwrqrq")
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
				log.Println("bad request")
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
					fmt.Println("HTTP Post timeout")
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
			log.Println(err)
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
			fmt.Println(receiveData, "receiveData")
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
				log.Println("no login")
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
					log.Println("HTTP Post timeout")
				} else {
					clientReturnBody.Msg = "request yunboss error"
					log.Println("request yunboss error")
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
			log.Println(err)
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
				log.Println("no login")
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
					log.Println("HTTP Post timeout")
				} else {
					clientReturnBody.Msg = "request yunboss error"
					log.Println("request yunboss error")
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
	log.Println("quit")
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
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

	fmt.Println(string(respBody))
	fmt.Printf("user: %s exit\n", user.UID)
}

func (s *Server) WSQuit(conn *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
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

	fmt.Println(string(respBody))
	fmt.Printf("user: %s exit\n", user.UID)
}
