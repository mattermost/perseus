package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type Pool struct {
	dsn string
	// This is a channel to acquire/release connections.
	conns chan *pgconn.PgConn
	// This holds all the connections.
	allConns    map[*pgconn.PgConn]struct{}
	allConnsMut sync.RWMutex

	logger *log.Logger
}

func NewPool(dsn string, logger *log.Logger) (*Pool, error) {
	poolSize := 3
	p := &Pool{
		dsn:      dsn,
		conns:    make(chan *pgconn.PgConn, poolSize),
		allConns: make(map[*pgconn.PgConn]struct{}),
		logger:   logger,
	}

	// var err error
	for i := 0; i < poolSize; i++ {
		conn, err := p.spawnConn()
		if err != nil {
			return nil, err
		}
		p.allConns[conn] = struct{}{}
		p.conns <- conn
	}

	return p, nil
}

// XXX: while calling acquireConn, clients will pass info
// like which Db, which schema, whether reader/writer etc.
// later this will be managed by pool manager. And pool manager
// will call into pool.
//
// For now, this does not create new conns, but later
// it has to.
func (p *Pool) AcquireConn() (*pgconn.PgConn, error) {
	conn := <-p.conns
	p.logger.Println("Got new conn")

	if err := conn.CheckConn(); err != nil {
		// conn.Close()
		// XXX: what to do if this fails?
		// close the connection, and remove from connSlice?
		return nil, err
	}

	return conn, nil
}

func (p *Pool) ReleaseConn(conn *pgconn.PgConn) {
	p.conns <- conn
	p.logger.Println("releasing conn")
}

func (p *Pool) DestroyConn(conn *pgconn.PgConn) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := conn.Close(ctx)

	p.allConnsMut.Lock()
	delete(p.allConns, conn)
	p.allConnsMut.Unlock()

	return err
}

func (p *Pool) Close() error {
	p.allConnsMut.Lock()
	defer p.allConnsMut.Unlock()

	for conn := range p.allConns {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := conn.Close(ctx)
		if err != nil {
			cancel()
			p.logger.Printf("Error closing backend connection: %v\n", err)
		}
		cancel()
	}
	return nil
}

func (p *Pool) spawnConn() (*pgconn.PgConn, error) {
	pgConn, err := pgconn.Connect(context.Background(), p.dsn)
	if err != nil {
		return nil, fmt.Errorf("pgconn failed to connect: %v", err)
	}

	if err := execQuery(pgConn, ";"); err != nil {
		// XXX: close conn here.
		return nil, fmt.Errorf("failed to ping: %v", err)
	}

	// XXX: Maybe better to hijack the connection here?
	// But only places are the access the raw conn and closing.

	return pgConn, nil
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
