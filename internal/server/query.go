package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

const (
	StatusUnset byte = 0
	StatusIdle  byte = 'I'
	StatusInTx  byte = 'T'
	StatusError byte = 'E'
)

type ClientConn struct {
	handle   *pgproto3.Backend
	txStatus byte
	logger   *log.Logger
	pool     *Pool

	// mut makes the getting of pool, and setting of the serverConn
	// atomic. This allows us to find the serverConn for a clientConn
	// to cancel a request.
	mut sync.Mutex
	// This is set to non-nil if there's an active transaction going on.
	serverConn *ServerConn

	schema string
}

func NewClientConn(handle *pgproto3.Backend, logger *log.Logger, pool *Pool, schema string) *ClientConn {
	return &ClientConn{
		handle: handle,
		logger: logger,
		pool:   pool,
		schema: schema,
	}
}

func (cc *ClientConn) handleQuery(feMsg pgproto3.FrontendMessage) error {
	// Leasing a connection
	if err := cc.acquireConn(); err != nil {
		return err
	}

	serverEnd := pgproto3.NewFrontend(cc.serverConn.Conn(), cc.serverConn.Conn())
	// serverEnd.Trace(cc.logger.Writer(), pgproto3.TracerOptions{})
	serverEnd.Send(feMsg)
	if err := serverEnd.Flush(); err != nil {
		return fmt.Errorf("error while flushing queryMsg: %w", err)
	}

	if err := cc.readBackendResponse(serverEnd); err != nil {
		return err
	}

	return nil
}

func (cc *ClientConn) handleExtendedQuery(feMsg pgproto3.FrontendMessage) error {
	// Leasing a connection
	if err := cc.acquireConn(); err != nil {
		return err
	}

	serverEnd := pgproto3.NewFrontend(cc.serverConn.Conn(), cc.serverConn.Conn())
	// serverEnd.Trace(cc.logger.Writer(), pgproto3.TracerOptions{})
	serverEnd.Send(feMsg)

	for {
		feMsg, err := cc.handle.Receive()
		if err != nil {
			return fmt.Errorf("error while receiving msg in extendedQuery: %w", err)
		}

		serverEnd.Send(feMsg)

		// Keep reading until we see a SYNC message
		_, ok := feMsg.(*pgproto3.Sync)
		if ok {
			break
		}
	}

	if err := serverEnd.Flush(); err != nil {
		return fmt.Errorf("error while flushing extendedQuery: %w", err)
	}

	if err := cc.readBackendResponse(serverEnd); err != nil {
		return err
	}

	return nil
}

func (cc *ClientConn) readBackendResponse(serverEnd *pgproto3.Frontend) error {
	// Read the response
	cnt := 0
	for {
		beMsg, err := serverEnd.Receive()
		if err != nil {
			return fmt.Errorf("error while receiving from server: %w", err)
		}
		cnt++

		switch typedMsg := beMsg.(type) {
		// Read all till ReadyForQuery
		case *pgproto3.ReadyForQuery:
			cc.handle.Send(typedMsg)
			if err := cc.handle.Flush(); err != nil {
				return fmt.Errorf("error while flushing to client: %w", err)
			}
			cc.txStatus = typedMsg.TxStatus

			// Releasing the conn back to the pool
			if cc.txStatus == StatusIdle {
				cc.mut.Lock()
				cc.pool.ReleaseConn(cc.serverConn)
				cc.serverConn = nil
				cc.mut.Unlock()
			}
			return nil
		default:
			cc.handle.Send(beMsg)
			// Flush if we have queued too many messages
			if cnt%10 == 0 {
				if err := cc.handle.Flush(); err != nil {
					return fmt.Errorf("error while flushing to client: %w", err)
				}
			}
			continue
		}
	}
}

func (cc *ClientConn) acquireConn() error {
	cc.mut.Lock()
	defer cc.mut.Unlock()
	if cc.serverConn != nil {
		return nil
	}

	conn, err := cc.pool.AcquireConn()
	if err != nil {
		return fmt.Errorf("error while acquiring conn: %w", err)
	}
	// We have just got a connection from the pool. First, we check
	// whether it's healthy or not.
	if err := conn.CheckConn(); err != nil {
		// *Server.handleConn will take care of closing the connection.
		return fmt.Errorf("error while checking conn: %w", err)
	}

	// This is a low-level method, so passing params is not really supported.
	// We need to implement sanitization ourselves. XXX: item for future.
	err = conn.Exec(fmt.Sprintf(`SET search_path='%s'`, cc.schema))
	if err != nil {
		return fmt.Errorf("error setting schema search path: %w", err)
	}

	cc.serverConn = conn
	return nil
}

func (cc *ClientConn) CancelServerConn() error {
	cc.mut.Lock()
	if cc.serverConn == nil {
		return nil
	}
	cc.mut.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(cc.pool.connCreateTimeout))
	defer cancel()
	return cc.serverConn.CancelRequest(ctx)
}
