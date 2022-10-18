package server

import (
	"encoding/json"
	"log"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

const (
	StatusUnset byte = 0
	StatusIdle  byte = 'I'
	StatusInTx  byte = 'T'
	StatusError byte = 'E'
)

type ClientConn struct {
	// rawConn  net.Conn
	handle   *pgproto3.Backend
	txStatus byte
	logger   *log.Logger
	pool     *Pool

	// This is set to non-nil if there's an active transaction going on.
	serverConn *pgconn.PgConn

	database string
	schema   string
}

func NewClientConn(cConn net.Conn, logger *log.Logger, pool *Pool) *ClientConn {
	return &ClientConn{
		// rawConn: cConn,
		handle: pgproto3.NewBackend(cConn, cConn),
		pool:   pool,
		logger: logger,
		// status:  StatusIdle,
	}
}

func (cc *ClientConn) handleQuery(feMsg pgproto3.FrontendMessage) error {
	// Leasing a connection
	if cc.serverConn == nil {
		conn, err := cc.pool.AcquireConn()
		if err != nil {
			return err
		}
		cc.serverConn = conn
	}
	serverEnd := pgproto3.NewFrontend(cc.serverConn.Conn(), cc.serverConn.Conn())
	serverEnd.Trace(cc.logger.Writer(), pgproto3.TracerOptions{})
	serverEnd.Send(feMsg)
	if err := serverEnd.Flush(); err != nil {
		return err
	}

	if err := cc.readBackendResponse(serverEnd); err != nil {
		return err
	}

	return nil
	// TODO: release the conn back to the pool here.
}

func (cc *ClientConn) handleExtendedQuery(feMsg pgproto3.FrontendMessage) error {
	// Leasing a connection
	if cc.serverConn == nil {
		conn, err := cc.pool.AcquireConn()
		if err != nil {
			return err
		}
		cc.serverConn = conn
	}
	serverEnd := pgproto3.NewFrontend(cc.serverConn.Conn(), cc.serverConn.Conn())
	serverEnd.Trace(cc.logger.Writer(), pgproto3.TracerOptions{})
	serverEnd.Send(feMsg)

	for {
		feMsg, err := cc.handle.Receive()
		if err != nil {
			return err
		}

		serverEnd.Send(feMsg)

		// Keep reading until we see a SYNC message
		_, ok := feMsg.(*pgproto3.Sync)
		if ok {
			break
		}
	}

	if err := serverEnd.Flush(); err != nil {
		return err
	}

	if err := cc.readBackendResponse(serverEnd); err != nil {
		return err
	}

	return nil
	// TODO: release the conn back to the pool here.
}

func (cc *ClientConn) readBackendResponse(serverEnd *pgproto3.Frontend) error {
	// Read the response
	cnt := 0
	for {
		beMsg, err := serverEnd.Receive()
		if err != nil {
			return err
		}
		cnt++

		switch typedMsg := beMsg.(type) {
		// Read all till ReadyForQuery
		case *pgproto3.ReadyForQuery:
			cc.handle.Send(typedMsg)
			if err := cc.handle.Flush(); err != nil {
				return err
			}
			cc.txStatus = typedMsg.TxStatus

			// Releasing the conn back to the pool
			if cc.txStatus == StatusIdle {
				cc.pool.ReleaseConn(cc.serverConn)
				cc.serverConn = nil
			}
			return nil
		default:
			cc.handle.Send(beMsg)
			// Flush if we have queued too many messages
			if cnt%10 == 0 {
				if err := cc.handle.Flush(); err != nil {
					return err
				}
			}
			continue
		}
	}
}

func (cc *ClientConn) handleStartup() error {
	startupMsg, err := cc.handle.ReceiveStartupMessage()
	if err != nil {
		return err
	}

	buf, err := json.Marshal(startupMsg)
	if err != nil {
		return err
	}

	cc.logger.Printf("[startup] %s\n", string(buf))

	switch typedMsg := startupMsg.(type) {
	case *pgproto3.StartupMessage:
		// TODO: Perform authentication here instead of blindly accepting everything
		cc.database = typedMsg.Parameters["database"]
		cc.schema = typedMsg.Parameters["schema_search_path"]
		cc.handle.Send(&pgproto3.AuthenticationOk{})
		cc.handle.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if err := cc.handle.Flush(); err != nil {
			return err
		}
	case *pgproto3.SSLRequest:
		cc.handle.Send(&denySSL{})
		if err := cc.handle.Flush(); err != nil {
			return err
		}
		return cc.handleStartup()
	}

	return nil
}

type denySSL struct {
}

func (*denySSL) Backend() {}

func (dst *denySSL) Decode(src []byte) error {
	return nil
}
func (src *denySSL) Encode(dst []byte) []byte {
	return append(dst, 'N')
}
