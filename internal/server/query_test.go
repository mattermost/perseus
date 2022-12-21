package server

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/mattermost/perseus/config"
	"github.com/mattermost/perseus/internal/pgmock"
)

func TestHandleQuery(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:")
	be.NilErr(t, err)
	serverErrChan := make(chan error, 1)
	defer ln.Close()

	var conn net.Conn
	defer func() { conn.Close() }()
	go func() {
		defer close(serverErrChan)

		var err error
		conn, err = ln.Accept()
		if err != nil {
			serverErrChan <- err
			return
		}

		err = conn.SetDeadline(time.Now().Add(time.Millisecond * 450))
		if err != nil {
			serverErrChan <- err
			return
		}

		backend := pgproto3.NewBackend(conn, conn)
		script := &pgmock.Script{Steps: pgmock.AcceptUnauthenticatedConnRequestSteps()}
		err = script.Run(backend)
		if err != nil {
			serverErrChan <- err
			return
		}
	}()

	parts := strings.Split(ln.Addr().String(), ":")
	host := parts[0]
	port := parts[1]
	connStr := fmt.Sprintf("sslmode=disable host=%s port=%s", host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pg_conn, err := pgconn.Connect(ctx, connStr)
	be.NilErr(t, err)
	be.NilErr(t, <-serverErrChan)

	lgr, err := setupLogging(config.Config{
		LogSettings: config.LogSettings{
			Level: "INFO",
		},
	})
	be.NilErr(t, err)
	p, err := NewPool(genBasePoolConfig())
	p.spawnConn = func(_ context.Context) (Conner, error) {
		return pg_conn, nil
	}
	be.NilErr(t, err)

	backend := pgproto3.NewBackend(conn, conn)
	cc := NewClientConn(backend, lgr, p, "test")
	// Setting the server conn to bypass cc.acquireConn(), because
	// that calls some methods of MultiResultReader.
	sc, err := p.AcquireConn()
	be.NilErr(t, err)
	be.Equal(t, 0, p.Stats().Idle)
	be.Equal(t, 1, p.Stats().InUse)
	cc.serverConn = sc

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var steps []pgmock.Step
		steps = append(steps,
			pgmock.ExpectMessage(&pgproto3.Query{String: "SELECT * from posts;"}),
			pgmock.SendMessage(&pgproto3.RowDescription{
				Fields: []pgproto3.FieldDescription{
					{
						Name:                 []byte("?column?"),
						TableOID:             0,
						TableAttributeNumber: 0,
						DataTypeOID:          23,
						DataTypeSize:         4,
						TypeModifier:         -1,
						Format:               0,
					},
				},
			}),
			pgmock.SendMessage(&pgproto3.DataRow{
				Values: [][]byte{[]byte("42")},
			}),
			pgmock.SendMessage(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")}),
			pgmock.SendMessage(&pgproto3.ReadyForQuery{TxStatus: 'I'}),
		)
		script := &pgmock.Script{Steps: steps}
		backend2 := pgproto3.NewBackend(conn, conn)
		be.NilErr(t, script.Run(backend2))
	}()

	be.NilErr(t, cc.handleQuery(&pgproto3.Query{
		String: "SELECT * from posts;",
	}))
	// Ensure that the connection is handed back.
	be.Equal(t, 1, p.Stats().Idle)
	be.Equal(t, 0, p.Stats().InUse)

	wg.Wait()
}
