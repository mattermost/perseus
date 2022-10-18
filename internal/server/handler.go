package server

import (
	"net"

	"github.com/jackc/pgx/v5/pgproto3"
)

func (s *Server) handleConn(c net.Conn) (err error) {
	cc := NewClientConn(c, s.logger, s.pool)
	if err := cc.handleStartup(); err != nil {
		return err
	}

	defer func() {
		if cc.serverConn != nil {
			if err != nil || cc.txStatus != StatusUnset /* This takes of care TxStatus = E */ {
				if err2 := cc.pool.DestroyConn(cc.serverConn); err2 != nil {
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
			return err
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
