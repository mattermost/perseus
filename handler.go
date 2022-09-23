package perseus

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/jackc/pgproto3/v2"
)

type schemaKey string

func (s *Server) serveConn(ctx context.Context, c *Conn) error {
	schema,  err := s.serveConnStartup(ctx, c)
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}

	for {
		msg, err := c.backend.Receive()
		if err != nil {
			return fmt.Errorf("receive message: %w", err)
		}

		log.Printf("[recv] %#v", msg)

		switch msg := msg.(type) {
		case *pgproto3.Query:
			if err := s.handleQueryMessage(ctx, schema, c, msg); err != nil {
				return fmt.Errorf("query message: %w", err)
			}

		case *pgproto3.Parse:
			if err := s.handleParseMessage(ctx, c, msg); err != nil {
				return fmt.Errorf("parse message: %w", err)
			}

		case *pgproto3.Sync: // ignore
			continue

		case *pgproto3.Terminate:
			return nil // exit

		default:
			return fmt.Errorf("unexpected message type: %#v", msg)
		}
	}
}

func (s *Server) serveConnStartup(ctx context.Context, c *Conn) (string, error) {
	msg, err := c.backend.ReceiveStartupMessage()
	if err != nil {
		return "", fmt.Errorf("receive startup message: %w", err)
	}

	switch msg := msg.(type) {
	case *pgproto3.StartupMessage:
		schema := getParameter(msg.Parameters, "search_path")
		if err := s.handleStartupMessage(ctx, c, msg); err != nil {
			return "", fmt.Errorf("startup message: %w", err)
		}
		return schema, nil
	case *pgproto3.SSLRequest:
		if schema, err := s.handleSSLRequestMessage(ctx, c, msg); err != nil {
			return schema, fmt.Errorf("ssl request message: %w", err)
		}
		return "", nil
	default:
		return "", fmt.Errorf("unexpected startup message: %#v", msg)
	}
}

func (s *Server) handleStartupMessage(ctx context.Context, c *Conn, msg *pgproto3.StartupMessage)  error {
	log.Printf("received startup message: %#v", msg)

		// Validate
	name := getParameter(msg.Parameters, "database")
	if name == "" {
		return writeMessages(c, &pgproto3.ErrorResponse{Message: "database required"})
	} else if strings.Contains(name, "..") {
		return writeMessages(c, &pgproto3.ErrorResponse{Message: "invalid database name"})
	}

	return writeMessages(c,
		&pgproto3.AuthenticationOk{},
		&pgproto3.ParameterStatus{Name: "server_version", Value: "14.0.0"},
		&pgproto3.ReadyForQuery{TxStatus: 'I'},
	)
}

func (s *Server) handleSSLRequestMessage(ctx context.Context, c *Conn, msg *pgproto3.SSLRequest) (string, error) {
	log.Printf("received ssl request message: %#v", msg)
	if _, err := c.Write([]byte("N")); err != nil {
		return "", err
	}
	return s.serveConnStartup(ctx, c)
}

func writeMessages(w io.Writer, msgs ...pgproto3.Message) error {
	var buf []byte
	for _, msg := range msgs {
		buf = msg.Encode(buf)
	}
	_, err := w.Write(buf)
	return err
}

func getParameter(m map[string]string, k string) string {
	if m == nil {
		return ""
	}
	return m[k]
}
