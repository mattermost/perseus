package perseus

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/jackc/pgproto3/v2"
	_ "github.com/lib/pq"
	"golang.org/x/sync/errgroup"
)

type Server struct {
	ln net.Listener

	mu    sync.Mutex
	conns map[*Conn]struct{}
	db    *sql.DB

	addr   string
	g      errgroup.Group
	ctx    context.Context
	cancel func()
}

type Conn struct {
	net.Conn
	backend *pgproto3.Backend
}

func NewServer(addr string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		conns:  make(map[*Conn]struct{}),
		addr:   addr,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) Start() error {
	var err error
	s.ln, err = net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	// dsn := "postgres://mmuser:mostest@localhost/loadtest?sslmode=disable&binary_parameters=yes"
	dsn := os.Getenv("PERSEUS_DSN")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}

	if err := db.Ping(); err != nil {
		return err
	}
	db.SetMaxOpenConns(5)
	s.db = db

	s.g.Go(func() error {
		if err := s.serve(); s.ctx.Err() != nil {
			fmt.Println(err)
			return err // return error unless context canceled
		}
		return nil
	})

	return nil
}

func (s *Server) Stop() (err error) {
	if s.ln != nil {
		if e := s.ln.Close(); err == nil {
			err = e
		}
	}

	s.cancel()

	// Track and close all open connections.
	if e := s.CloseClientConnections(); err == nil {
		err = e
	}

	if err := s.g.Wait(); err != nil {
		return err
	}
	return err
}

func (s *Server) serve() error {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return fmt.Errorf("from accept: %w", err)
		}
		conn := newConn(c)

		// Track live connections.
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		log.Println("connection accepted: ", conn.RemoteAddr())

		s.g.Go(func() error {
			defer s.CloseClientConnection(conn)

			if err := s.serveConn(s.ctx, conn); err != nil {
				log.Printf("connection error, closing: %s", err)
				return nil
			}

			log.Printf("connection closed: %s", conn.RemoteAddr())
			return nil
		})
	}
}

func newConn(conn net.Conn) *Conn {
	return &Conn{
		Conn:    conn,
		backend: pgproto3.NewBackend(pgproto3.NewChunkReader(conn), conn),
	}
}

// CloseClientConnections disconnects all Postgres connections.
func (s *Server) CloseClientConnections() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for conn := range s.conns {
		if e := conn.Close(); err == nil {
			err = e
		}
	}

	s.conns = make(map[*Conn]struct{})

	return err
}

// CloseClientConnection disconnects a Postgres connections.
func (s *Server) CloseClientConnection(conn *Conn) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.conns, conn)
	return conn.Close()
}
