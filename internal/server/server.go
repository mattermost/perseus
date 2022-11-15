package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/agnivade/perseus/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server contains all the necessary information to run Perseus
type Server struct {
	cfg    config.Config
	logger *log.Logger

	wg           sync.WaitGroup
	ln           net.Listener
	connMut      sync.Mutex
	connMap      map[net.Conn]struct{}
	clientConnWg sync.WaitGroup

	authPool *pgxpool.Pool
	poolMgr  *PoolManager
}

// New creates a new Perseus server
func New(cfg config.Config) (*Server, error) {
	s := &Server{
		cfg:     cfg,
		logger:  log.New(os.Stdout, "[perseus] ", log.Lshortfile|log.LstdFlags),
		connMap: make(map[net.Conn]struct{}),
	}

	s.logger.Println("Initializing server..")
	l, err := net.Listen("tcp", s.cfg.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("error trying to listen on %s: %w", s.cfg.ListenAddress, err)
	}
	s.ln = l

	poolCfg, err := pgxpool.ParseConfig(s.cfg.AuthDBSettings.AuthDBDSN)
	if err != nil {
		return nil, fmt.Errorf("error trying to parse pool config %s: %w", s.cfg.AuthDBSettings.AuthDBDSN, err)
	}
	authPool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("error initializing auth pool: %w", err)
	}
	s.authPool = authPool

	s.poolMgr, err = NewPoolManager(s.cfg, s.logger)
	if err != nil {
		return nil, fmt.Errorf("error initializing pool manager: %w", err)
	}

	return s, nil
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
		s.clientConnWg.Add(1)
		go func(c net.Conn) {
			defer func() {
				// Closing the connection
				c.Close()

				// Deleting the entry from connMap
				s.connMut.Lock()
				delete(s.connMap, conn)
				s.connMut.Unlock()

				s.clientConnWg.Done()
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

func (s *Server) Reload(cfg config.Config) {
	s.logger.Println("Reloading config.. ")
	s.poolMgr.Reload(cfg)
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
		delete(s.connMap, conn)
	}
	s.connMut.Unlock()

	// Wait till all client connections are closed.
	s.clientConnWg.Wait()

	if err := s.poolMgr.Close(); err != nil {
		s.logger.Printf("Error closing pool manager: %v\n", err)
	}
}
