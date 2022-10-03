package server

import (
	"github.com/jackc/pgx/v5/pgproto3"
)

func (s *Server) handleQuery(feMsg pgproto3.FrontendMessage, clientEnd *pgproto3.Backend) error {

	// TODO: acquireConn logic needs to be here

	serverEnd := pgproto3.NewFrontend(s.backendConn.Conn(), s.backendConn.Conn())
	serverEnd.Trace(s.logger.Writer(), pgproto3.TracerOptions{})
	serverEnd.Send(feMsg)
	if err := serverEnd.Flush(); err != nil {
		return err
	}

	if err := readBackendResponse(serverEnd, clientEnd); err != nil {
		return err
	}

	return nil
	// TODO: release the conn back to the pool here.
}

func (s *Server) handleExtendedQuery(feMsg pgproto3.FrontendMessage, clientEnd *pgproto3.Backend) error {

	// TODO: acquireConn logic needs to be here

	serverEnd := pgproto3.NewFrontend(s.backendConn.Conn(), s.backendConn.Conn())
	serverEnd.Trace(s.logger.Writer(), pgproto3.TracerOptions{})
	serverEnd.Send(feMsg)
	for {
		feMsg, err := clientEnd.Receive()
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

	if err := readBackendResponse(serverEnd, clientEnd); err != nil {
		return err
	}

	return nil
	// TODO: release the conn back to the pool here.
}

func readBackendResponse(serverEnd *pgproto3.Frontend, clientEnd *pgproto3.Backend) error {
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
			clientEnd.Send(typedMsg)
			if err := clientEnd.Flush(); err != nil {
				return err
			}
			// typedMsg.TxStatus
			return nil
		default:
			clientEnd.Send(beMsg)
			// Flush if we have queued too many messages
			if cnt%10 == 0 {
				if err := clientEnd.Flush(); err != nil {
					return err
				}
			}
			continue
		}
	}
}
