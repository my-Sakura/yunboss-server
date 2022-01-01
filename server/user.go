package server

import (
	"net"

	"github.com/gorilla/websocket"
)

type User struct {
	UID    string
	Conn   net.Conn
	WSConn *websocket.Conn
	Token  string
}

type UserOption func(*User)

func (u *User) SetUserOption(opts ...UserOption) {
	for _, opt := range opts {
		opt(u)
	}
}

func NewUser(uid string, opts ...UserOption) *User {
	user := &User{
		UID: uid,
	}

	user.SetUserOption(opts...)

	return user
}

func WithUID(uid string) UserOption {
	return func(user *User) {
		user.UID = uid
	}
}

func WithConn(conn net.Conn) UserOption {
	return func(user *User) {
		user.Conn = conn
	}
}

func WithWSConn(conn *websocket.Conn) UserOption {
	return func(user *User) {
		user.WSConn = conn
	}
}

func WithToken(token string) UserOption {
	return func(user *User) {
		user.Token = token
	}
}
