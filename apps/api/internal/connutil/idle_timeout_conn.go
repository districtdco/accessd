package connutil

import (
	"net"
	"time"
)

type idleTimeoutConn struct {
	net.Conn
	idleTimeout time.Duration
}

func WrapIdleTimeout(conn net.Conn, idleTimeout time.Duration) net.Conn {
	if conn == nil || idleTimeout <= 0 {
		return conn
	}
	return &idleTimeoutConn{
		Conn:        conn,
		idleTimeout: idleTimeout,
	}
}

func (c *idleTimeoutConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(c.idleTimeout))
	return c.Conn.Read(p)
}

func (c *idleTimeoutConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(c.idleTimeout))
	return c.Conn.Write(p)
}
