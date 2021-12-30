package msgserver

import (
	"net"

	"github.com/gorilla/websocket"
)

type User struct {
	UID    string
	Socket *websocket.Conn
	Conn   net.Conn
	Token  string
}

type UserOption func(*User)

func (u *User) SetUserOption(opts ...UserOption) {
	for _, opt := range opts {
		opt(u)
	}
}

func NewUser(uid string, conn net.Conn, opts ...UserOption) *User {
	user := &User{
		UID:  uid,
		Conn: conn,
	}

	for _, opt := range opts {
		opt(user)
	}

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

func WithToken(token string) UserOption {
	return func(user *User) {
		user.Token = token
	}
}
