package msgserver

import (
	"bytes"
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

	"github.com/spf13/viper"
)

type Server struct {
	UsersByUID   *sync.Map
	UsersByConn  *sync.Map
	Config       *Config
	Done         chan struct{}
	LoginCh      chan *ClientLoginBody
	HeartBeatCh  chan *ClientHeartBeatBody
	ReceiveCh    chan *ClientPushBody
	PushReturnCh chan *ServerReturnBody
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

type ServerOption func(*Server)

func NewServer() *Server {
	config := &Config{}
	if err := viper.Unmarshal(config); err != nil {
		panic(err)
	}
	server := &Server{
		Config:       config,
		UsersByUID:   &sync.Map{},
		UsersByConn:  &sync.Map{},
		Done:         make(chan struct{}),
		LoginCh:      make(chan *ClientLoginBody),
		HeartBeatCh:  make(chan *ClientHeartBeatBody),
		ReceiveCh:    make(chan *ClientPushBody),
		PushReturnCh: make(chan *ServerReturnBody),
	}

	return server
}

func (s *Server) SetOption(opts ...ServerOption) {
	for _, opt := range opts {
		opt(s)
	}
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

		// if err = conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		// 	panic(err)
		// }

		go s.Handler(conn)
	}
}

func (s *Server) Handler(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
		}
	}()

	go s.Login(conn)
	go s.HeartBeat(conn)
	go s.ReceiveMsg(conn)

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
			heartBeatRequest := &ClientHeartBeatBody{}
			if err = json.Unmarshal(buf[:n], heartBeatRequest); err != nil {
				panic(err)
			}

			s.HeartBeatCh <- heartBeatRequest

		case "clientpush":
			clientPush := &ClientPushBody{}
			if err = json.Unmarshal(buf[:n], clientPush); err != nil {
				panic(err)
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

func (s *Server) Login(conn net.Conn) {
	const (
		url = "http://139.199.60.49:2180/oservice/client/login"
	)
	receiveData := <-s.LoginCh
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

	user := NewUser(receiveData.Uid, conn, WithToken(loginResponse.Token))
	s.UsersByUID.Store(user.UID, user)
	s.UsersByConn.Store(conn, user)
}

func (s *Server) HeartBeat(conn net.Conn) {
	const (
		url = "http://139.199.60.49:2180/oservice/client/heartbeat"
	)

	for {
		select {
		case receiveData := <-s.HeartBeatCh:
			var user *User
			if u, ok := s.UsersByUID.Load(receiveData.UID); ok {
				user = u.(*User)
			} else {
				panic("load user failed")
			}
			body, err := json.Marshal(receiveData.Body)
			if err != nil {
				panic(err)
			}
			req := struct {
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
				if _, err = user.Conn.Write([]byte("overtime")); err != nil {
					panic(err)
				}
				return
			}
			defer resp.Body.Close()

			respBody, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				panic(err)
			}

			heartBeatResponse := &ServerHeartBeatBody{}
			if err := json.Unmarshal(respBody, &heartBeatResponse); err != nil {
				panic(err)
			}
			heartBeatResponse.Type = "heartbeat"

			response, err := json.Marshal(heartBeatResponse)
			if err != nil {
				panic(err)
			}

			if _, err = user.Conn.Write(response); err != nil {
				panic(err)
			}

		case <-s.Done:
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

func (s *Server) ReceiveMsg(conn net.Conn) {
	const (
		url = "http://139.199.60.49:2180/oservice/client/msg"
	)

	for {
		select {
		case receiveData := <-s.ReceiveCh:
			fmt.Println(receiveData, "receiveData")
			var user *User
			if u, ok := s.UsersByUID.Load(receiveData.UID); ok {
				user = u.(*User)
			} else {
				panic("load user failed")
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
			resp, err := http.DefaultClient.Do(request)
			if err != nil {
				panic(err)
			}
			defer resp.Body.Close()

			respBody, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				panic(err)
			}

			ReceiveResponse := &ClientPushBody{}
			if err = json.Unmarshal(respBody, &ReceiveResponse); err != nil {
				panic(err)
			}

			ReceiveResponse.Type = "clientpush"
			r, err := json.Marshal(ReceiveResponse)
			if err != nil {
				panic(err)
			}
			if _, err = user.Conn.Write(r); err != nil {
				panic(err)
			}

		case <-s.Done:
			return
		}
	}
}

func (s *Server) Quit(conn net.Conn) {
	const (
		url = "http://139.199.60.49:2180/oservice/client/quit"
	)

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
