package server

import (
	"log"
	"net"
	"os"
	"sync"
)

// Server contains all the necessary information to run Perseus
type Server struct {
	cfg    Config
	logger *log.Logger

	wg           sync.WaitGroup
	ln           net.Listener
	connMut      sync.Mutex
	connMap      map[net.Conn]struct{}
	clientConnWg sync.WaitGroup

	poolMgr *PoolManager
}

// New creates a new Perseus server
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

	s.poolMgr = NewPoolManager(s.cfg.DBSettings.WriterDSN, s.logger)

	return nil
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
