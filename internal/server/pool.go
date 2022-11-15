package server

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Pool is a copied implementation of just the connection pooling
// logic from database/sql.
type Pool struct {
	logger    *log.Logger
	spawnConn func(ctx context.Context) (Conner, error)

	mu           sync.Mutex    // protects following fields
	freeConn     []*ServerConn // free connections ordered by returnedAt oldest to newest
	connRequests map[uint64]chan connRequest
	nextRequest  uint64 // Next key to use in connRequests.
	numOpen      int    // number of opened and pending open connections

	// Used to signal the need for new connections
	// a goroutine running connectionOpener() reads on this chan and
	// maybeOpenNewConnections sends on the chan (one send per needed connection)
	// It is closed during db.Close(). The close tells the connectionOpener
	// goroutine to exit.
	openerCh chan struct{}
	closed   bool

	maxIdle           int           // zero means defaultMaxIdleConns; negative means 0
	maxOpen           int           // <= 0 means unlimited
	maxLifetime       time.Duration // maximum amount of time a connection may be reused
	maxIdleTime       time.Duration // maximum amount of time a connection may be idle before being closed
	connCreateTimeout time.Duration
	connCloseTimeout  time.Duration
	schemaExecTimeout time.Duration
	cleanerCh         chan struct{}
	waitCount         int64        // Total number of connections waited for.
	maxIdleClosed     int64        // Total number of connections closed due to idle count.
	maxIdleTimeClosed int64        // Total number of connections closed due to idle time.
	maxLifetimeClosed int64        // Total number of connections closed due to max connection lifetime limit.
	waitDuration      atomic.Int64 // Total time waited for new connections.

	stop func() // stop cancels the connection opener.
}

type PoolConfig struct {
	SpawnConn func(ctx context.Context) (Conner, error)
	Logger    *log.Logger

	MaxIdle           int
	MaxOpen           int
	MaxLifetime       time.Duration
	MaxIdleTime       time.Duration
	ConnCreateTimeout time.Duration
	ConnCloseTimeout  time.Duration
	SchemaExecTimeout time.Duration
}

// This is the size of the connectionOpener request chan (Pool.openerCh).
// This value should be larger than the maximum typical value
// used for pool.maxOpen. If maxOpen is significantly larger than
// connectionRequestQueueSize then it is possible for ALL calls into the *Pool
// to block until the connectionOpener can satisfy the backlog of requests.
var connectionRequestQueueSize = 1000000

var (
	ErrPoolClosed  = errors.New("pool is closed")
	ErrConnExpired = errors.New("connection expired")
)

func NewPool(cfg PoolConfig) (*Pool, error) {
	if cfg.MaxIdle <= 0 {
		return nil, errors.New("cfg.MaxIdleCount has to be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		spawnConn:         cfg.SpawnConn,
		logger:            cfg.Logger,
		maxIdle:           cfg.MaxIdle,
		maxOpen:           cfg.MaxOpen,
		maxLifetime:       cfg.MaxLifetime,
		maxIdleTime:       cfg.MaxIdleTime,
		connCreateTimeout: cfg.ConnCreateTimeout,
		connCloseTimeout:  cfg.ConnCloseTimeout,
		schemaExecTimeout: cfg.SchemaExecTimeout,

		openerCh:     make(chan struct{}, connectionRequestQueueSize),
		connRequests: make(map[uint64]chan connRequest),
		stop:         cancel,
	}

	go p.connectionOpener(ctx)

	return p, nil
}

// connRequest represents one request for a new connection
// When there are no idle connections available, DB.conn will create
// a new connRequest and put it on the db.connRequests list.
type connRequest struct {
	conn *ServerConn
	err  error
}

// Runs in a separate goroutine, opens new connections when requested.
func (p *Pool) connectionOpener(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.openerCh:
			p.openNewConnection(ctx)
		}
	}
}

// Open one new connection
func (p *Pool) openNewConnection(ctx context.Context) {
	// maybeOpenNewConnections has already executed p.numOpen++ before it sent
	// on p.openerCh. This function must execute p.numOpen-- if the
	// connection fails or is closed before returning.
	conn, err := p.spawnConn(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		if err == nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), p.connCloseTimeout)
			defer closeCancel()
			conn.Close(closeCtx)
		}
		p.numOpen--
		return
	}
	if err != nil {
		p.numOpen--
		p.putConnDBLocked(nil, err)
		// Deviation from database/sql. We don't want to open a new connection
		// if creating one already failed.
		return
	}
	sc := &ServerConn{
		pool:       p,
		createdAt:  time.Now(),
		returnedAt: time.Now(),
		conn:       conn,
	}
	if !p.putConnDBLocked(sc, err) {
		p.numOpen--
		closeCtx, closeCancel := context.WithTimeout(context.Background(), p.connCloseTimeout)
		defer closeCancel()
		conn.Close(closeCtx)
	}
}

// Satisfy a connRequest or put the serverConn in the idle pool.
// putConnDBLocked will satisfy a connRequest if there is one, or it will
// return the *serverConn to the freeConn list if err == nil and the idle
// connection limit will not be exceeded.
// If err != nil, the value of sc is ignored.
// If err == nil, then sc must not equal nil.
// If a connRequest was fulfilled or the *serverConn was placed in the
// freeConn list, then true is returned, otherwise false is returned.
func (p *Pool) putConnDBLocked(sc *ServerConn, err error) bool {
	if p.closed {
		return false
	}
	if p.maxOpen > 0 && p.numOpen > p.maxOpen {
		return false
	}
	if c := len(p.connRequests); c > 0 {
		var req chan connRequest
		var reqKey uint64
		for reqKey, req = range p.connRequests {
			break
		}
		delete(p.connRequests, reqKey) // Remove from pending requests.
		if err == nil {
			sc.inUse = true
		}
		req <- connRequest{
			conn: sc,
			err:  err,
		}
		return true
	} else if err == nil && !p.closed {
		// if there is space for more idle conns
		// then add it.
		if p.maxIdle > len(p.freeConn) {
			p.freeConn = append(p.freeConn, sc)
			p.startCleanerLocked()
			return true
		}
		p.maxIdleClosed++
	}
	return false
}

func (p *Pool) shortestIdleTimeLocked() time.Duration {
	if p.maxIdleTime <= 0 {
		return p.maxLifetime
	}
	if p.maxLifetime <= 0 {
		return p.maxIdleTime
	}

	min := p.maxIdleTime
	if min > p.maxLifetime {
		min = p.maxLifetime
	}
	return min
}

// Assumes db.mu is locked.
// If there are connRequests and the connection limit hasn't been reached,
// then tell the connectionOpener to open new connections.
func (p *Pool) maybeOpenNewConnections() {
	numRequests := len(p.connRequests)
	if p.maxOpen > 0 {
		numCanOpen := p.maxOpen - p.numOpen
		if numRequests > numCanOpen {
			numRequests = numCanOpen
		}
	}
	for numRequests > 0 {
		p.numOpen++ // optimistically
		numRequests--
		if p.closed {
			return
		}
		p.openerCh <- struct{}{}
	}
}

// putConn adds a connection to the db's free pool.
func (p *Pool) ReleaseConn(sc *ServerConn) {
	p.mu.Lock()
	if !sc.inUse {
		p.mu.Unlock()
		panic("perseus: connection returned that was never out")
	}

	var closeConn bool
	if sc.expired(p.maxLifetime) {
		p.maxLifetimeClosed++
		closeConn = true
	}
	sc.inUse = false
	sc.returnedAt = time.Now()

	if closeConn {
		// Don't reuse bad connections.
		// Since the conn is considered bad and is being discarded, treat it
		// as closed. Don't decrement the open count here, finalClose will
		// take care of that.
		p.maybeOpenNewConnections()
		p.mu.Unlock()
		sc.Close()
		return
	}
	added := p.putConnDBLocked(sc, nil)
	p.mu.Unlock()

	if !added {
		sc.Close()
		return
	}
}

// connReuseStrategy determines how (*DB).conn returns database connections.
type connReuseStrategy uint8

const (
	// alwaysNewConn forces a new connection to the database.
	alwaysNewConn connReuseStrategy = iota
	// cachedOrNewConn returns a cached connection, if available, else waits
	// for one to become available (if MaxOpenConns has been reached) or
	// creates a new database connection.
	cachedOrNewConn
)

func (p *Pool) AcquireConn() (*ServerConn, error) {
	// The first time might run into an expired connection,
	// so we give a second chance.
	for i := 0; i < 2; i++ {
		sc, err := p.conn(cachedOrNewConn)
		// only return if connection is not expired, then probably
		// something else has happened
		if err == nil || !errors.Is(err, ErrConnExpired) {
			return sc, err
		}
	}

	return p.conn(alwaysNewConn)
}

// conn returns a newly-opened or cached *ServerConn.
func (p *Pool) conn(strategy connReuseStrategy) (*ServerConn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	lifetime := p.maxLifetime

	// Prefer a free connection, if possible.
	last := len(p.freeConn) - 1
	if strategy == cachedOrNewConn && last >= 0 {
		// Reuse the lowest idle time connection so we can close
		// connections which remain idle as soon as possible.
		conn := p.freeConn[last]
		p.freeConn = p.freeConn[:last]
		conn.inUse = true
		if conn.expired(lifetime) {
			p.maxLifetimeClosed++
			p.mu.Unlock()
			conn.Close()
			return nil, ErrConnExpired
		}
		p.mu.Unlock()

		return conn, nil
	}

	// CacheMiss

	// Out of free connections or we were asked not to use one. If we're not
	// allowed to open any more connections, make a request and wait.
	if p.maxOpen > 0 && p.numOpen >= p.maxOpen {
		// Make the connRequest channel. It's buffered so that the
		// connectionOpener doesn't block while waiting for the req to be read.
		req := make(chan connRequest, 1)
		reqKey := p.nextRequestKeyLocked()
		p.connRequests[reqKey] = req
		p.waitCount++
		p.mu.Unlock()

		waitStart := time.Now()

		ret, ok := <-req
		p.waitDuration.Add(int64(time.Since(waitStart)))

		if !ok {
			return nil, ErrPoolClosed
		}
		// Only check if the connection is expired if the strategy is cachedOrNewConns.
		// If we require a new connection, just re-use the connection without looking
		// at the expiry time. If it is expired, it will be checked when it is placed
		// back into the connection pool.
		// This prioritizes giving a valid connection to a client over the exact connection
		// lifetime, which could expire exactly after this point anyway.
		if strategy == cachedOrNewConn && ret.err == nil && ret.conn.expired(lifetime) {
			p.mu.Lock()
			p.maxLifetimeClosed++
			p.mu.Unlock()
			ret.conn.Close()
			return nil, ErrConnExpired
		}
		if ret.conn == nil {
			return nil, ret.err
		}

		return ret.conn, ret.err
	}

	p.numOpen++ // optimistically
	p.mu.Unlock()
	conn, err := p.spawnConn(context.Background())
	if err != nil {
		p.mu.Lock()
		p.numOpen-- // correct for earlier optimism
		// Deviation from database/sql. We don't want to open a new connection
		// if creating one already failed.
		p.mu.Unlock()
		return nil, err
	}
	p.mu.Lock()
	sc := &ServerConn{
		pool:       p,
		createdAt:  time.Now(),
		returnedAt: time.Now(),
		conn:       conn,
		inUse:      true,
	}
	p.mu.Unlock()
	return sc, nil
}

// nextRequestKeyLocked returns the next connection request key.
// It is assumed that nextRequest will not overflow.
func (p *Pool) nextRequestKeyLocked() uint64 {
	next := p.nextRequest
	p.nextRequest++
	return next
}

func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed { // Make p.Close idempotent
		p.mu.Unlock()
		return nil
	}
	if p.cleanerCh != nil {
		close(p.cleanerCh)
	}
	var err error
	fns := make([]func() error, 0, len(p.freeConn))
	for _, sc := range p.freeConn {
		fns = append(fns, sc.closeDBLocked())
	}
	p.freeConn = nil
	p.closed = true
	for _, req := range p.connRequests {
		close(req)
	}
	p.mu.Unlock()
	for _, fn := range fns {
		err1 := fn()
		if err1 != nil {
			err = err1
		}
	}
	p.stop()
	return err
}

// startCleanerLocked starts connectionCleaner if needed.
func (p *Pool) startCleanerLocked() {
	if (p.maxLifetime > 0 || p.maxIdleTime > 0) && p.numOpen > 0 && p.cleanerCh == nil {
		p.cleanerCh = make(chan struct{}, 1)
		go p.connectionCleaner(p.shortestIdleTimeLocked())
	}
}

func (p *Pool) connectionCleaner(d time.Duration) {
	const minInterval = time.Second

	if d < minInterval {
		d = minInterval
	}
	t := time.NewTimer(d)

	for {
		select {
		case <-t.C:
		case <-p.cleanerCh: // maxLifetime was changed or db was closed.
		}

		p.mu.Lock()

		d = p.shortestIdleTimeLocked()
		if p.closed || p.numOpen == 0 || d <= 0 {
			p.cleanerCh = nil
			p.mu.Unlock()
			return
		}

		d, closing := p.connectionCleanerRunLocked(d)
		p.mu.Unlock()
		for _, c := range closing {
			c.Close()
		}

		if d < minInterval {
			d = minInterval
		}

		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(d)
	}
}

// connectionCleanerRunLocked removes connections that should be closed from
// freeConn and returns them along side an updated duration to the next check
// if a quicker check is required to ensure connections are checked appropriately.
func (p *Pool) connectionCleanerRunLocked(d time.Duration) (time.Duration, []*ServerConn) {
	var idleClosing int64
	var closing []*ServerConn
	if p.maxIdleTime > 0 {
		// As freeConn is ordered by returnedAt process
		// in reverse order to minimise the work needed.
		idleSince := time.Now().Add(-p.maxIdleTime)
		last := len(p.freeConn) - 1
		for i := last; i >= 0; i-- {
			c := p.freeConn[i]
			// If the conn has been returned longer
			// then the maxIdleTime
			if c.returnedAt.Before(idleSince) {
				i++
				closing = p.freeConn[:i:i]
				p.freeConn = p.freeConn[i:]
				idleClosing = int64(len(closing))
				p.maxIdleTimeClosed += idleClosing
				break
			}
		}

		if len(p.freeConn) > 0 {
			c := p.freeConn[0]
			if d2 := c.returnedAt.Sub(idleSince); d2 < d {
				// Ensure idle connections are cleaned up as soon as
				// possible.
				d = d2
			}
		}
	}

	if p.maxLifetime > 0 {
		expiredSince := time.Now().Add(-p.maxLifetime)
		for i := 0; i < len(p.freeConn); i++ {
			c := p.freeConn[i]
			if c.createdAt.Before(expiredSince) {
				closing = append(closing, c)

				last := len(p.freeConn) - 1
				// Use slow delete as order is required to ensure
				// connections are reused least idle time first.
				copy(p.freeConn[i:], p.freeConn[i+1:])
				p.freeConn[last] = nil
				p.freeConn = p.freeConn[:last]
				i--
			} else if d2 := c.createdAt.Sub(expiredSince); d2 < d {
				// Prevent connections sitting the freeConn when they
				// have expired by updating our next deadline d.
				d = d2
			}
		}
		p.maxLifetimeClosed += int64(len(closing)) - idleClosing
	}

	return d, closing
}

func (p *Pool) Reload(new PoolConfig) {
	if p.maxOpen != new.MaxOpen {
		p.SetMaxOpenConns(new.MaxOpen)
	}

	if p.maxIdle != new.MaxIdle {
		p.SetMaxIdleConns(new.MaxIdle)
	}

	if p.maxLifetime != new.MaxLifetime {
		p.SetConnMaxLifetime(new.MaxLifetime)
	}

	if p.maxIdleTime != new.MaxIdleTime {
		p.SetConnMaxIdleTime(new.MaxIdleTime)
	}
}

// SetMaxIdleConns sets the maximum number of connections in the idle
// connection pool.
//
// If MaxOpenConns is greater than 0 but less than the new MaxIdleConns,
// then the new MaxIdleConns will be reduced to match the MaxOpenConns limit.
//
// If n <= 0, no idle connections are retained.
//
// The default max idle connections is currently 2. This may change in
// a future release.
func (p *Pool) SetMaxIdleConns(n int) {
	p.logger.Println("setting max idle")
	p.mu.Lock()
	if n > 0 {
		p.maxIdle = n
	} else {
		// No idle connections.
		p.maxIdle = -1
	}
	// Make sure maxIdle doesn't exceed maxOpen
	if p.maxOpen > 0 && p.maxIdle > p.maxOpen {
		p.maxIdle = p.maxOpen
	}
	var closing []*ServerConn
	idleCount := len(p.freeConn)
	maxIdle := p.maxIdle
	if idleCount > maxIdle {
		closing = p.freeConn[maxIdle:]
		p.freeConn = p.freeConn[:maxIdle]
	}
	p.maxIdleClosed += int64(len(closing))
	p.mu.Unlock()
	for _, c := range closing {
		c.Close()
	}
}

// SetMaxOpenConns sets the maximum number of open connections to the database.
//
// If MaxIdleConns is greater than 0 and the new MaxOpenConns is less than
// MaxIdleConns, then MaxIdleConns will be reduced to match the new
// MaxOpenConns limit.
//
// If n <= 0, then there is no limit on the number of open connections.
// The default is 0 (unlimited).
func (p *Pool) SetMaxOpenConns(n int) {
	p.mu.Lock()
	p.maxOpen = n
	if n < 0 {
		p.maxOpen = 0
	}
	syncMaxIdle := p.maxOpen > 0 && p.maxIdle > p.maxOpen
	p.mu.Unlock()
	if syncMaxIdle {
		p.SetMaxIdleConns(n)
	}
}

// SetConnMaxLifetime sets the maximum amount of time a connection may be reused.
//
// Expired connections may be closed lazily before reuse.
//
// If d <= 0, connections are not closed due to a connection's age.
func (p *Pool) SetConnMaxLifetime(d time.Duration) {
	if d < 0 {
		d = 0
	}
	p.mu.Lock()
	// Wake cleaner up when lifetime is shortened.
	if d > 0 && d < p.maxLifetime && p.cleanerCh != nil {
		select {
		case p.cleanerCh <- struct{}{}:
		default:
		}
	}
	p.maxLifetime = d
	p.startCleanerLocked()
	p.mu.Unlock()
}

// SetConnMaxIdleTime sets the maximum amount of time a connection may be idle.
//
// Expired connections may be closed lazily before reuse.
//
// If d <= 0, connections are not closed due to a connection's idle time.
func (p *Pool) SetConnMaxIdleTime(d time.Duration) {
	if d < 0 {
		d = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	// Wake cleaner up when idle time is shortened.
	if d > 0 && d < p.maxIdleTime && p.cleanerCh != nil {
		select {
		case p.cleanerCh <- struct{}{}:
		default:
		}
	}
	p.maxIdleTime = d
	p.startCleanerLocked()
}

// DBStats contains database statistics.
type DBStats struct {
	MaxOpenConnections int // Maximum number of open connections to the database.

	// Pool Status
	OpenConnections int // The number of established connections both in use and idle.
	InUse           int // The number of connections currently in use.
	Idle            int // The number of idle connections.

	// Counters
	WaitCount         int64         // The total number of connections waited for.
	WaitDuration      time.Duration // The total time blocked waiting for a new connection.
	MaxIdleClosed     int64         // The total number of connections closed due to SetMaxIdleConns.
	MaxIdleTimeClosed int64         // The total number of connections closed due to SetConnMaxIdleTime.
	MaxLifetimeClosed int64         // The total number of connections closed due to SetConnMaxLifetime.
}

// Stats returns database statistics.
func (p *Pool) Stats() DBStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := DBStats{
		MaxOpenConnections: p.maxOpen,

		Idle:            len(p.freeConn),
		OpenConnections: p.numOpen,
		InUse:           p.numOpen - len(p.freeConn),

		WaitCount:         p.waitCount,
		WaitDuration:      time.Duration(p.waitDuration.Load()),
		MaxIdleClosed:     p.maxIdleClosed,
		MaxIdleTimeClosed: p.maxIdleTimeClosed,
		MaxLifetimeClosed: p.maxLifetimeClosed,
	}
	return stats
}
