package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type ServerConn struct {
	conn *pgconn.PgConn
	pool *Pool

	createdAt time.Time

	sync.Mutex // guards following
	closed     bool

	// guarded by pool.mu
	inUse      bool
	returnedAt time.Time // Time the connection was created or returned.
}

func (sc *ServerConn) Conn() net.Conn {
	return sc.conn.Conn()
}

func (sc *ServerConn) expired(timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	return sc.createdAt.Add(timeout).Before(time.Now())
}

func (sc *ServerConn) closeDBLocked() func() error {
	sc.Lock()
	defer sc.Unlock()
	if sc.closed {
		return func() error { return errors.New("sql: duplicate Conn close") }
	}
	sc.closed = true
	return sc.finalClose
}

func (sc *ServerConn) Close() error {
	sc.Lock()
	if sc.closed {
		sc.Unlock()
		return errors.New("sql: duplicate Conn close")
	}
	sc.closed = true
	sc.Unlock() // not defer; finalClose calls may need to lock
	return sc.finalClose()
}

func (sc *ServerConn) finalClose() error {
	var err error

	withLock(sc, func() {
		ctx, cancel := context.WithTimeout(context.Background(), sc.pool.connCloseTimeout)
		defer cancel()
		err = sc.conn.Close(ctx)
		sc.conn = nil
	})

	sc.pool.mu.Lock()
	sc.pool.numOpen--
	sc.pool.maybeOpenNewConnections()
	sc.pool.mu.Unlock()

	return err
}

// withLock runs while holding lk.
func withLock(lk sync.Locker, fn func()) {
	lk.Lock()
	defer lk.Unlock() // in case fn panics
	fn()
}
