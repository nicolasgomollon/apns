package apns

import (
	"container/list"
	"crypto/tls"
	"io"
	"log"
	"time"
)

type buffer struct {
	size int
	*list.List
}

func newBuffer(size int) *buffer {
	return &buffer{size, list.New()}
}

func (b *buffer) Add(v interface{}) *list.Element {
	e := b.PushBack(v)

	if b.Len() > b.size {
		b.Remove(b.Front())
	}

	return e
}

type Client struct {
	Conn         *Conn
	FailedNotifs chan NotificationResult
	Sent         int
	Failed       int
	Len          int
	Verbose      bool

	notifs chan Notification
	id     uint32
}

func newClientWithConn(gw string, conn Conn, verbose bool) *Client {
	c := &Client{
		Conn:         &conn,
		FailedNotifs: make(chan NotificationResult),
		Sent:         0,
		Failed:       0,
		Len:          0,
		Verbose:      verbose,
		id:           uint32(1),
		notifs:       make(chan Notification),
	}

	go c.runLoop()

	return c
}

func NewClientWithCert(gw string, cert tls.Certificate, args ...bool) *Client {
	verbose := false
	for _, v := range args {
		verbose = v
		break
	}
	conn := NewConnWithCert(gw, cert)
	return newClientWithConn(gw, conn, verbose)
}

func NewClient(gw string, cert string, key string, args ...bool) (*Client, error) {
	verbose := false
	for _, v := range args {
		verbose = v
		break
	}
	conn, err := NewConn(gw, cert, key)
	if err != nil {
		return nil, err
	}
	return newClientWithConn(gw, conn, verbose), nil
}

func NewClientWithFiles(gw string, certFile string, keyFile string, args ...bool) (*Client, error) {
	verbose := false
	for _, v := range args {
		verbose = v
		break
	}
	conn, err := NewConnWithFiles(gw, certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return newClientWithConn(gw, conn, verbose), nil
}

func (c *Client) logln(v ...interface{}) {
	if c.Verbose {
		log.Println(v...)
	}
}

func (c *Client) logf(s string, v ...interface{}) {
	if c.Verbose {
		log.Printf(s, v...)
	}
}

func (c *Client) Send(n Notification) {
	c.logln("Added notification to push queue.")
	c.Len++
	c.notifs <- n
}

func (c *Client) reportFailedPush(v interface{}, err *Error) {
	failedNotif, ok := v.(Notification)
	if !ok || v == nil {
		return
	}

	select {
	case c.FailedNotifs <- NotificationResult{Notif: failedNotif, Err: *err}:
	default:
	}
}

func (c *Client) requeue(cursor *list.Element) {
	// If `cursor` is not nil, this means there are notifications that
	// need to be delivered (or redelivered)
	for ; cursor != nil; cursor = cursor.Next() {
		if n, ok := cursor.Value.(Notification); ok {
			go func() { c.notifs <- n }()
		}
	}
}

func (c *Client) handleError(err *Error, buffer *buffer) *list.Element {
	cursor := buffer.Back()

	for cursor != nil {
		// Get notification
		n, _ := cursor.Value.(Notification)

		// If the notification, move cursor after the trouble notification
		if n.Identifier == err.Identifier {
			go c.reportFailedPush(cursor.Value, err)

			next := cursor.Next()

			buffer.Remove(cursor)
			return next
		}

		cursor = cursor.Prev()
	}

	return cursor
}

func (c *Client) runLoop() {
	sent := newBuffer(50)
	cursor := sent.Front()

	// APNS connection
	for {
		err := c.Conn.Connect()
		if err != nil {
			// TODO Probably want to exponentially backoff...
			time.Sleep(1 * time.Second)
			continue
		}

		// Start reading errors from APNS
		errs := readErrs(c.Conn)

		c.requeue(cursor)

		// Connection open, listen for notifs and errors
		for {
			var err error
			var n Notification

			// Check for notifications or errors. There is a chance we'll send notifications
			// if we already have an error since `select` will "pseudorandomly" choose a
			// ready channels. It turns out to be fine because the connection will already
			// be closed and it'll requeue. We could check before we get to this select
			// block, but it doesn't seem worth the extra code and complexity.
			c.logln("Waiting for channel input...")
			select {
			case err = <-errs:
				break
			case n = <-c.notifs:
				notificationPayloadBytes, _ := n.Payload.MarshalJSON()
				notificationPayload := string(notificationPayloadBytes)
				c.logf("Incoming notification to %v: %v\n", n.DeviceToken, notificationPayload)
				break
			}

			// Check if there is an error we understand.
			if nErr, ok := err.(*Error); ok {
				c.logf("APNS ERROR %v: %v\n", nErr.Status, nErr.ErrStr)
				if (2 <= nErr.Status) && (nErr.Status <= 8) {
					// The notification is malformed in some way, and resending it won't help.
					c.Sent--
					c.Failed++
					continue
				} else {
					// Find the notification that failed, move the cursor right after it.
					cursor = c.handleError(nErr, sent)
					break
				}
			}

			if err != nil {
				c.logln("Received error:", err.Error())
				break
			}

			// Add to list
			cursor = sent.Add(n)

			// Set identifier if not specified
			if n.Identifier == 0 {
				n.Identifier = c.id
				c.id++
			} else if c.id < n.Identifier {
				c.id = n.Identifier + 1
			}

			// Build binary representation of notification.
			b, err := n.ToBinary()
			if err != nil {
				// Building the binary failed in some way, so skip it.
				c.Failed++
				cursor = cursor.Next()
				c.logln("Error building binary for notification:", err.Error())
				continue
			}

			// Write the notification binary to the APNS connection.
			_, err = c.Conn.Write(b)

			if err == io.EOF {
				c.logln("Received EOF trying to write notification.")
				break
			}

			if err != nil {
				c.logln("Error writing to APNS connection:", err.Error())
				break
			}

			c.logln("Successfully pushed notification!")
			c.Sent++
			cursor = cursor.Next()
		}
	}
}

func readErrs(c *Conn) chan error {
	errs := make(chan error)

	go func() {
		p := make([]byte, 6, 6)
		_, err := c.Read(p)
		if err != nil {
			errs <- err
			return
		}

		e := NewError(p)
		errs <- &e
	}()

	return errs
}
