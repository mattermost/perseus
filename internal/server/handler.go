package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

type startupParams struct {
	username string
	database string
	schema   string
	password string
}

type AuthRow struct {
	id                 int
	source_db          string
	source_schema      string
	source_user        string
	source_pass_hashed string
	dest_host          string
	dest_user          string
	dest_db            string
	dest_pass_enc      string
}

func (s *Server) handleConn(c net.Conn) (err error) {
	handle := pgproto3.NewBackend(c, c)
	params, err := handleStartup(handle)
	if err != nil {
		return err
	}

	if params.database == "" {
		msg := "empty database name received in params"
		handle.Send(&pgproto3.ErrorResponse{Message: msg})
		handle.Flush()
		return errors.New(msg)
	}

	if params.schema == "" {
		msg := "empty schema name received in params"
		handle.Send(&pgproto3.ErrorResponse{Message: msg})
		handle.Flush()
		return errors.New(msg)
	}

	if params.username == "" {
		msg := "empty user name received in params"
		handle.Send(&pgproto3.ErrorResponse{Message: msg})
		handle.Flush()
		return errors.New(msg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(s.cfg.AuthDBSettings.AuthQueryTimeoutSecs))
	defer cancel()
	var row AuthRow
	err = s.authPool.
		QueryRow(ctx, "SELECT id, source_db, source_schema, source_user, source_pass_hashed,		dest_host, dest_user, dest_db, dest_pass_enc FROM perseus_auth WHERE source_db=$1 AND source_schema=$2", params.database, params.schema).
		Scan(&row.id, &row.source_db, &row.source_schema, &row.source_user, &row.source_pass_hashed, &row.dest_host, &row.dest_user, &row.dest_db, &row.dest_pass_enc)
	if err != nil {
		msg := fmt.Sprintf("error querying the auth table: %v", err)
		handle.Send(&pgproto3.ErrorResponse{Message: msg})
		handle.Flush()
		return errors.New(msg)
	}

	// TODO: hash the password and subtle time compare
	if params.password != row.source_pass_hashed {
		return errors.New("invalid client password passed")
	}

	handle.Send(&pgproto3.AuthenticationOk{})
	handle.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if err := handle.Flush(); err != nil {
		return fmt.Errorf("error while flushing authOK: %w", err)
	}

	pool, err := s.poolMgr.GetOrCreatePool(row)
	if err != nil {
		return fmt.Errorf("error while acquiring a pool: %w", err)
	}
	cc := NewClientConn(handle, s.logger, pool, params.schema)

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

	switch typedMsg := startupMsg.(type) {
	case *pgproto3.StartupMessage:
		// We send in cleartext because we hash with a better
		// algorithm than MD5. Ideally, we should use SCRAM.
		handle.Send(&pgproto3.AuthenticationCleartextPassword{})
		if err := handle.Flush(); err != nil {
			return nil, fmt.Errorf("error while flushing authPasswd: %w", err)
		}

		pass, err := handle.Receive()
		if err != nil {
			return nil, err
		}
		typedPass, ok := pass.(*pgproto3.PasswordMessage)
		if !ok {
			return nil, errors.New("didn't receive password message")
		}
		return &startupParams{
			username: typedMsg.Parameters["user"],
			database: typedMsg.Parameters["database"],
			schema:   typedMsg.Parameters["schema_search_path"],
			password: typedPass.Password,
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
