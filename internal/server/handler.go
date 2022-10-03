package server

import (
	"encoding/json"
	"errors"
	"net"

	"github.com/jackc/pgx/v5/pgproto3"
)

var errUnknownMsg = errors.New("unknown message")

func (s *Server) handleConn(c net.Conn) error {
	clientEnd := pgproto3.NewBackend(c, c) // acts as the proxy for client conn
	if err := s.handleStartup(clientEnd); err != nil {
		return err
	}

	// enter command cycle
	for {
		feMsg, err := clientEnd.Receive()
		if err != nil {
			return err
		}

		switch feMsg.(type) {
		case *pgproto3.Query:
			if err := s.handleQuery(feMsg, clientEnd); err != nil {
				return err
			}
		case *pgproto3.Parse,
			*pgproto3.Bind:
			if err := s.handleExtendedQuery(feMsg, clientEnd); err != nil {
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

func (s *Server) handleStartup(clientEnd *pgproto3.Backend) error {
	startupMsg, err := clientEnd.ReceiveStartupMessage()
	if err != nil {
		return err
	}

	buf, err := json.Marshal(startupMsg)
	if err != nil {
		return err
	}

	s.logger.Printf("[startup] %s\n", string(buf))

	switch startupMsg.(type) {
	case *pgproto3.StartupMessage:
		// TODO: Perform authentication here instead of blindly accepting everything
		clientEnd.Send(&pgproto3.AuthenticationOk{})
		clientEnd.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if err := clientEnd.Flush(); err != nil {
			return err
		}
	case *pgproto3.SSLRequest:
		clientEnd.Send(&denySSL{})
		if err := clientEnd.Flush(); err != nil {
			return err
		}
		return s.handleStartup(clientEnd)
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
