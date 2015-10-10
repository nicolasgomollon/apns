package apns

import (
	"container/list"
	"crypto/tls"
	"log"
	"sync"
	"time"
)

type buffer struct {
	size int
	*list.List
	sync.Mutex
}

func newBuffer(size int) *buffer {
	return &buffer{
		size: size,
		List: list.New(),
	}
}

func (b *buffer) Add(v interface{}) *list.Element {
	b.Lock()
	defer b.Unlock()
	e := b.PushBack(v)

	if b.Len() > b.size {
		b.Remove(b.Front())
	}

	return e
}

type Client struct {
	Conn         *Conn
	FailedNotifs chan NotificationResult

	sent    *buffer
	id      uint32
	verbose bool

	// concurrency
	idm          sync.Mutex
	reconnecting bool
	cond         *sync.Cond
}

func newClientWithConn(gw string, conn Conn, verbose bool) *Client {
	c := &Client{
		Conn:         &conn,
		FailedNotifs: make(chan NotificationResult),
		sent:         newBuffer(1000),
		id:           uint32(1),
		verbose:      verbose,
		cond:         sync.NewCond(&sync.Mutex{}),
	}

	c.reconnect()

	return c
}

func NewClientWithCert(gw string, cert tls.Certificate, verbose bool) *Client {
	conn := NewConnWithCert(gw, cert)

	return newClientWithConn(gw, conn, verbose)
}

func NewClient(gw string, cert string, key string, verbose bool) (*Client, error) {
	conn, err := NewConn(gw, cert, key)
	if err != nil {
		return nil, err
	}

	return newClientWithConn(gw, conn, verbose), nil
}

func NewClientWithFiles(gw string, certFile string, keyFile string, verbose bool) (*Client, error) {
	conn, err := NewConnWithFiles(gw, certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return newClientWithConn(gw, conn, verbose), nil
}

func (c *Client) log(v ...interface{}) {
	if c.verbose {
		log.Println(v...)
	}
}

func (c *Client) Send(n Notification) {
	// Set identifier if not specified
	c.idm.Lock()
	if n.Identifier == 0 {
		n.Identifier = c.id
		c.id++
	} else if c.id < n.Identifier {
		c.id = n.Identifier + 1
	}
	c.idm.Unlock()

	b, err := n.ToBinary()
	if err != nil {
		c.log("Could not convert APNS notification to binary:", err)
		return
	}

	// Check for reconnection in progress just before send
	c.cond.L.Lock()
	for c.reconnecting {
		c.cond.Wait()
	}
	c.cond.L.Unlock()

	if _, err := c.Conn.Write(b); err != nil {
		c.log("Error writing to APNS connection:", err)
		// reconnect the socket
		c.reconnect()

		// send this notification again
		c.Send(n)
		return
	}

	// Add to sent list after successful send
	c.sent.Add(n)

	return
}

func (c *Client) reconnect() {
	var cont bool
	c.cond.L.Lock()
	if !c.reconnecting {
		c.reconnecting = true
		cont = true
	}
	c.cond.L.Unlock()

	if !cont {
		return
	}

	for {
		if err := c.Conn.Connect(); err != nil {
			// TODO Probably want to exponentially backoff...
			c.log("Error connecting to APNS, will retry:", err)
			time.Sleep(time.Second)
			continue
		}
		break
	}

	c.log("Connection to APNS (re-)established.")

	go c.readErrs()

	c.cond.L.Lock()
	c.reconnecting = false
	c.cond.L.Unlock()
	c.cond.Broadcast()
}

func (c *Client) readErrs() {
	p := make([]byte, 6, 6)
	_, err := c.Conn.Read(p)

	// Error encountered, reconnect
	go c.reconnect()

	// If the error was just some transport error, log and move on
	if err != nil {
		// If EOF immediately after connecting, make sure you are hitting
		// the correct gateway for your cert
		c.log("APNS connection error:", err)
		return
	}

	// We got an APNS error-response packet

	// Lock the sent list so we can iterate over it and remove
	// the offending notification without interference
	c.sent.Lock()
	defer c.sent.Unlock()

	apnsErr := NewError(p)
	var resend *list.Element

	for cursor := c.sent.Back(); cursor != nil; cursor = cursor.Prev() {
		// Get notification
		n, _ := cursor.Value.(Notification)

		// If the notification id matches, move cursor after the trouble notification
		if n.Identifier == apnsErr.Identifier {
			c.log("APNS notification failed:", apnsErr.Error(), n.ID)
			go c.reportFailedPush(n, apnsErr)
			resend = cursor.Next()
			c.sent.Remove(cursor)
			break
		}
	}

	for ; resend != nil; resend = resend.Next() {
		if n, ok := resend.Value.(Notification); ok {
			go c.Send(n)
		}
	}
}

func (c *Client) reportFailedPush(n Notification, err Error) {
	select {
	case c.FailedNotifs <- NotificationResult{n, err}:
	default:
	}
}
