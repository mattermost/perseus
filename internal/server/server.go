package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Server contains all the necessary information to run Bifrost
type Server struct {
	cfg    Config
	logger *log.Logger

	wg      sync.WaitGroup
	ln      net.Listener
	connMap map[net.Conn]struct{}
	connMut sync.Mutex

	backendConn *pgconn.PgConn
}

// New creates a new Bifrost server
func New(cfg Config) *Server {
	s := &Server{
		cfg:     cfg,
		logger:  log.New(os.Stdout, "[perseus] ", log.Lshortfile|log.LstdFlags),
		connMap: make(map[net.Conn]struct{}),
	}

	return s
}

// Initialize the server
func (s *Server) Initialize() error {
	s.logger.Println("Initializing server..")
	l, err := net.Listen("tcp", s.cfg.ListenAddress)
	if err != nil {
		return err
	}

	s.ln = l

	// Place this in AcquireConn
	pgConn, err := pgconn.Connect(context.Background(), s.cfg.DBSettings.WriterDSN)
	if err != nil {
		return fmt.Errorf("pgconn failed to connect: %v", err)
	}

	if err := execQuery(pgConn, ";"); err != nil {
		return fmt.Errorf("failed to ping: %v", err)
	}

	// XXX: Maybe better to hijack the connection here?
	// But only places are the access the raw conn and closing.
	s.backendConn = pgConn

	return nil
}

func execQuery(pgConn *pgconn.PgConn, sql string) error {
	mrr := pgConn.Exec(context.Background(), sql)
	var err error
	for mrr.NextResult() {
		_, err = mrr.ResultReader().Close()
	}
	err = mrr.Close()
	return err
}

func (s *Server) AcceptConns() error {
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		// Wait for a connection.
		conn, err := s.ln.Accept()
		if err != nil {
			return err
		}

		// Handle the connection in a new goroutine.
		go func(c net.Conn) {
			defer func() {
				// Closing the connection
				c.Close()

				// Deleting the entry from connMap
				s.connMut.Lock()
				delete(s.connMap, conn)
				s.connMut.Unlock()
			}()

			s.logger.Println("Accepting new connection")
			// Populating the conn map.
			s.connMut.Lock()
			s.connMap[conn] = struct{}{}
			s.connMut.Unlock()

			if err := s.handleConn(c); err != nil {
				s.logger.Printf("error while handling conn: %v\n", err)
			}
		}(conn)
	}
}

// Stop stops the server
func (s *Server) Stop() {
	s.logger.Println("Shutting down server..")

	if err := s.ln.Close(); err != nil {
		s.logger.Printf("error closing listener %v\n", err)
	}

	// Wait till accept loop exited
	s.wg.Wait()

	// Shutdown all client connections
	s.connMut.Lock()
	for conn := range s.connMap {
		if err := conn.Close(); err != nil {
			s.logger.Printf("Error closing client connection: %v\n", err)
		}
	}
	s.connMut.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.backendConn.Close(ctx); err != nil {
		s.logger.Printf("Error closing backend connection: %v\n", err)
	}

	return
}
