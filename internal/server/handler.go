package server

import (
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgproto3"
)

type startupParams struct {
	database string
	schema   string
	readOnly bool
}

func (s *Server) handleConn(c net.Conn) (err error) {
	handle := pgproto3.NewBackend(c, c)
	params, err := handleStartup(handle)
	if err != nil {
		return err
	}

	// TODO: pass readonly here as well.
	// or maybe pass the full params
	pool, err := s.poolMgr.GetOrCreatePool(params.database, params.schema)
	if err != nil {
		return fmt.Errorf("error while acquiring a pool: %w", err)
	}
	cc := NewClientConn(handle, s.logger, pool)

	defer func() {
		if cc.serverConn != nil {
			if err != nil || cc.txStatus != StatusUnset /* This takes of care TxStatus = E */ {
				if err2 := cc.serverConn.Close(); err2 != nil {
					s.logger.Printf("Error while destroying conn: %v\n", err2)
				}
				cc.serverConn = nil
			}
		}
	}()

	// enter command cycle
	var feMsg pgproto3.FrontendMessage
	for {
		// resetting txStatus
		cc.txStatus = StatusUnset

		feMsg, err = cc.handle.Receive()
		if err != nil {
			return fmt.Errorf("error while receiving from client conn: %w", err)
		}

		switch feMsg.(type) {
		case *pgproto3.Query:
			err = cc.handleQuery(feMsg)
			if err != nil {
				return err
			}
		case *pgproto3.Parse,
			*pgproto3.Bind:
			err = cc.handleExtendedQuery(feMsg)
			if err != nil {
				return err
			}
		case *pgproto3.Terminate:
			s.logger.Println("Received terminate msg, closing connection")
			return nil
		default:
			s.logger.Printf("Received some other msg: %T, cannot handle. Closing\n", feMsg)
			return nil
		}
	}
}

func handleStartup(handle *pgproto3.Backend) (*startupParams, error) {
	startupMsg, err := handle.ReceiveStartupMessage()
	if err != nil {
		return nil, fmt.Errorf("error while receiving startup message: %w", err)
	}

	// buf, err := json.Marshal(startupMsg)
	// if err != nil {
	// 	return err
	// }

	// cc.logger.Printf("[startup] %s\n", string(buf))

	switch typedMsg := startupMsg.(type) {
	case *pgproto3.StartupMessage:
		// TODO: Perform authentication here instead of blindly accepting everything
		handle.Send(&pgproto3.AuthenticationOk{})
		handle.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if err := handle.Flush(); err != nil {
			return nil, fmt.Errorf("error while flushing authOK: %w", err)
		}
		return &startupParams{
			database: typedMsg.Parameters["database"],
			schema:   typedMsg.Parameters["schema_search_path"],
		}, nil
	case *pgproto3.SSLRequest:
		handle.Send(&denySSL{})
		if err := handle.Flush(); err != nil {
			return nil, fmt.Errorf("error while flushing denySSL: %w", err)
		}
		return handleStartup(handle)
	}

	return nil, fmt.Errorf("unexpected startup msg: %T", startupMsg)
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
