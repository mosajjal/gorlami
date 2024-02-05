// websocket pipe
package main

import (
	"net/http"
	"time"

	"github.com/gofiber/websocket/v2"
	gorilla "github.com/gorilla/websocket"
)

func WebsocketProxy(c *websocket.Conn, endpoint string) error {
	// connect to upstream

	headers := http.Header{}
	headers.Add("Authorization", "Basic a2FzbV91c2VyOmhlYWRsZXNz")
	headers.Add("Sec-WebSocket-Origin", "172.17.0.1:3000")
	headers.Add("Sec-WebSocket-Protocol", "binary")

	dialer := gorilla.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second
	// dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	conn, _, err := dialer.Dial(endpoint, headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	pipeErr := make(chan error, 4)
	// pipe messages
	go func() {
		for {
			t, msg, err := conn.ReadMessage()
			if err != nil {
				pipeErr <- err
				return
			}
			c.WriteMessage(t, msg)
		}
	}()
	go func() {
		for {
			t, msg, err := c.ReadMessage()
			if err != nil {
				pipeErr <- err
				return
			}
			conn.WriteMessage(t, msg)
		}
	}()
	return <-pipeErr
}
