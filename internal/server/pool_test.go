package server

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPoolSpike(t *testing.T) {
	cfg := genBasePoolConfig()
	cfg.MaxOpen = 5
	cfg.MaxIdle = 5

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()
	sc, err := p.AcquireConn()
	be.NilErr(rt, err)
	be.Equal(rt, 0, p.Stats().Idle)
	p.ReleaseConn(sc)
	be.Equal(rt, 1, p.Stats().Idle)

	var wg sync.WaitGroup
	connChan := make(chan *ServerConn)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sc, err := p.AcquireConn()
			be.NilErr(rt, err)
			connChan <- sc

			stats := p.Stats()
			be.True(rt, stats.OpenConnections <= 5)
			be.True(rt, stats.InUse <= 5)
		}(i)
	}

	// Wait to let some goroutines wait
	// and ensure that none can acquire without releasing.
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 10; i++ {
		p.ReleaseConn(<-connChan)
	}

	wg.Wait()

	be.Equal(rt, 5, p.Stats().Idle)
}

func TestMaxOpenConnsUpdate(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		cfg := genBasePoolConfig()
		cfg.MaxOpen = 3
		cfg.MaxIdle = 2

		rt := be.Relaxed(t)

		p, err := NewPool(cfg)
		be.NilErr(rt, err)
		defer func() { be.NilErr(rt, p.Close()) }()

		conn0, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn1, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn2, err := p.AcquireConn()
		be.NilErr(rt, err)

		be.Equal(t, 3, p.Stats().OpenConnections)

		p.SetMaxOpenConns(2)

		p.ReleaseConn(conn0)
		p.ReleaseConn(conn1)

		// Ensure that extra connections are not added back.
		be.Equal(t, 2, p.Stats().OpenConnections)

		p.ReleaseConn(conn2)

		be.Equal(t, 2, p.Stats().OpenConnections)
	})

	t.Run("maxOpen < maxIdle", func(t *testing.T) {
		cfg := genBasePoolConfig()
		cfg.MaxOpen = 3
		cfg.MaxIdle = 3

		rt := be.Relaxed(t)

		p, err := NewPool(cfg)
		be.NilErr(rt, err)
		defer func() { be.NilErr(rt, p.Close()) }()

		conn0, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn1, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn2, err := p.AcquireConn()
		be.NilErr(rt, err)

		be.Equal(t, 3, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)

		p.ReleaseConn(conn0)
		p.ReleaseConn(conn1)
		p.ReleaseConn(conn2)

		be.Equal(t, 3, p.Stats().OpenConnections)
		be.Equal(t, 3, p.Stats().Idle)

		p.SetMaxOpenConns(2)

		be.Equal(t, 2, p.Stats().OpenConnections)
		be.Equal(t, 2, p.Stats().Idle)
	})
}

func TestMaxIdleConnsUpdate(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		cfg := genBasePoolConfig()
		cfg.MaxOpen = 3
		cfg.MaxIdle = 3

		rt := be.Relaxed(t)

		p, err := NewPool(cfg)
		be.NilErr(rt, err)
		defer func() { be.NilErr(rt, p.Close()) }()

		conn0, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn1, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn2, err := p.AcquireConn()
		be.NilErr(rt, err)

		be.Equal(t, 3, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)

		p.ReleaseConn(conn0)
		p.ReleaseConn(conn1)
		p.ReleaseConn(conn2)

		be.Equal(t, 3, p.Stats().OpenConnections)
		be.Equal(t, 3, p.Stats().Idle)

		p.SetMaxIdleConns(1)

		be.Equal(t, 1, p.Stats().OpenConnections)
		be.Equal(t, 1, p.Stats().Idle)
	})

	t.Run("maxIdle > maxOpen", func(t *testing.T) {
		cfg := genBasePoolConfig()
		cfg.MaxOpen = 2
		cfg.MaxIdle = 2

		rt := be.Relaxed(t)

		p, err := NewPool(cfg)
		be.NilErr(rt, err)
		defer func() { be.NilErr(rt, p.Close()) }()

		conn0, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn1, err := p.AcquireConn()
		be.NilErr(rt, err)

		be.Equal(t, 2, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)

		p.ReleaseConn(conn0)
		p.ReleaseConn(conn1)

		be.Equal(t, 2, p.Stats().OpenConnections)
		be.Equal(t, 2, p.Stats().Idle)

		p.SetMaxIdleConns(5)

		be.Equal(t, 2, p.maxIdle)
	})
}

func TestMaxIdleTime(t *testing.T) {
	cfg := genBasePoolConfig()
	cfg.MaxOpen = 3
	cfg.MaxIdle = 3
	cfg.MaxIdleTime = 2 * time.Second

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()

	conn0, err := p.AcquireConn()
	be.NilErr(rt, err)

	conn1, err := p.AcquireConn()
	be.NilErr(rt, err)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 0, p.Stats().Idle)

	p.ReleaseConn(conn0)
	p.ReleaseConn(conn1)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 2, p.Stats().Idle)

	time.Sleep(cfg.MaxIdleTime + time.Second)

	be.Equal(t, 0, p.Stats().OpenConnections)
	be.Equal(t, 0, p.Stats().Idle)
	be.Equal(t, 2, p.Stats().MaxIdleTimeClosed)
}

func TestMaxIdleTimeUpdate(t *testing.T) {
	cfg := genBasePoolConfig()
	cfg.MaxOpen = 3
	cfg.MaxIdle = 3
	cfg.MaxIdleTime = time.Minute

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()

	now := time.Now()
	conn0, err := p.AcquireConn()
	be.NilErr(rt, err)

	conn1, err := p.AcquireConn()
	be.NilErr(rt, err)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 0, p.Stats().Idle)

	p.ReleaseConn(conn0)
	p.ReleaseConn(conn1)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 2, p.Stats().Idle)

	time.Sleep(100 * time.Millisecond)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 2, p.Stats().Idle)

	p.SetConnMaxIdleTime(100 * time.Millisecond)

	time.Sleep(p.maxIdleTime + time.Second)

	be.Equal(t, 0, p.Stats().OpenConnections)
	be.Equal(t, 0, p.Stats().Idle)
	be.Equal(t, 2, p.Stats().MaxIdleTimeClosed)
	be.True(t, time.Since(now) < cfg.MaxIdleTime)

}

func TestMaxLifeTime(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		cfg := genBasePoolConfig()
		cfg.MaxOpen = 3
		cfg.MaxIdle = 3
		cfg.MaxLifetime = 1 * time.Second
		cfg.MaxIdleTime = cfg.MaxLifetime * 10

		rt := be.Relaxed(t)

		p, err := NewPool(cfg)
		be.NilErr(rt, err)
		defer func() { be.NilErr(rt, p.Close()) }()

		conn0, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn1, err := p.AcquireConn()
		be.NilErr(rt, err)

		be.Equal(t, 2, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)

		p.ReleaseConn(conn0)
		p.ReleaseConn(conn1)

		be.Equal(t, 2, p.Stats().OpenConnections)
		be.Equal(t, 2, p.Stats().Idle)

		time.Sleep(cfg.MaxLifetime + time.Second)

		be.Equal(t, 0, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)
		be.Equal(t, 2, p.Stats().MaxLifetimeClosed)
	})

	t.Run("release conn", func(t *testing.T) {
		cfg := genBasePoolConfig()
		cfg.MaxOpen = 3
		cfg.MaxIdle = 3
		cfg.MaxLifetime = 1 * time.Second
		cfg.MaxIdleTime = cfg.MaxLifetime * 10

		rt := be.Relaxed(t)

		p, err := NewPool(cfg)
		be.NilErr(rt, err)
		defer func() { be.NilErr(rt, p.Close()) }()

		conn0, err := p.AcquireConn()
		be.NilErr(rt, err)

		conn1, err := p.AcquireConn()
		be.NilErr(rt, err)

		be.Equal(t, 2, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)

		time.Sleep(cfg.MaxLifetime + time.Second)

		p.ReleaseConn(conn0)
		p.ReleaseConn(conn1)

		be.Equal(t, 0, p.Stats().OpenConnections)
		be.Equal(t, 0, p.Stats().Idle)
		be.Equal(t, 2, p.Stats().MaxLifetimeClosed)
	})
}

func TestMaxLifetimeUpdate(t *testing.T) {
	cfg := genBasePoolConfig()
	cfg.MaxOpen = 3
	cfg.MaxIdle = 3
	cfg.MaxLifetime = 1 * time.Second
	cfg.MaxIdleTime = cfg.MaxLifetime * 10

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()

	conn0, err := p.AcquireConn()
	be.NilErr(rt, err)

	conn1, err := p.AcquireConn()
	be.NilErr(rt, err)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 0, p.Stats().Idle)

	p.ReleaseConn(conn0)
	p.ReleaseConn(conn1)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 2, p.Stats().Idle)

	time.Sleep(100 * time.Millisecond)

	be.Equal(t, 2, p.Stats().OpenConnections)
	be.Equal(t, 2, p.Stats().Idle)

	p.SetConnMaxLifetime(100 * time.Millisecond)

	time.Sleep(p.maxLifetime + time.Second)

	be.Equal(t, 0, p.Stats().OpenConnections)
	be.Equal(t, 0, p.Stats().Idle)
	be.Equal(t, 2, p.Stats().MaxLifetimeClosed)
}

func TestMaxOpenConnBlock(t *testing.T) {
	cfg := genBasePoolConfig()
	cfg.MaxOpen = 2

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()

	conn0, err := p.AcquireConn()
	be.NilErr(rt, err)

	conn1, err := p.AcquireConn()
	be.NilErr(rt, err)

	var wg sync.WaitGroup
	connChan := make(chan *ServerConn, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc, err2 := p.AcquireConn()
		be.NilErr(rt, err2)
		connChan <- sc
	}()

	// Wait to let the goroutine wait
	// and ensure that it cannot acquire without releasing.
	time.Sleep(100 * time.Millisecond)
	be.Equal(t, 2, p.Stats().OpenConnections)

	p.SetMaxOpenConns(3)
	// allow goroutine to acquire the conn.
	p.ReleaseConn(conn0)
	wg.Wait()

	be.Equal(t, 1, p.Stats().WaitCount)
	be.True(t, p.Stats().WaitDuration >= 100*time.Millisecond)

	// Acquire another one
	conn0, err = p.AcquireConn()
	be.NilErr(rt, err)

	be.Equal(t, 3, p.Stats().OpenConnections)

	p.ReleaseConn(<-connChan)
	p.ReleaseConn(conn1)
	p.ReleaseConn(conn0)
}

func TestReloadPool(t *testing.T) {
	cfg := PoolConfig{
		SpawnConn: func(ctx context.Context) (Conner, error) {
			return &connMock{}, nil
		},
		MaxOpen:     2,
		MaxIdle:     5,
		MaxLifetime: 2 * time.Second,
		MaxIdleTime: 5 * time.Second,
	}

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()

	cfg.MaxOpen = 4
	cfg.MaxIdle = 10
	cfg.MaxLifetime = 10 * time.Second
	cfg.MaxIdleTime = 8 * time.Second

	p.Reload(cfg)

	be.Equal(t, 4, p.maxOpen)
	be.Equal(t, 4, p.maxIdle) // maxIdle cannot be > maxOpen
	be.Equal(t, 10*time.Second, p.maxLifetime)
	be.Equal(t, 8*time.Second, p.maxIdleTime)
}

// What we are trying to test here is set a limited number of max
// connections, and then make a new connection request wait.
// Now we sleep till the maxLifetime expires, and then we release the
// conn. This will immediately close the released connection
// and test the p.openerCh code path.
func TestOpenPendingConnWithReleaseExpiry(t *testing.T) {
	cfg := genBasePoolConfig()
	cfg.MaxOpen = 2
	cfg.MaxIdle = 2
	cfg.MaxLifetime = 1 * time.Second
	cfg.MaxIdleTime = cfg.MaxLifetime * 10

	rt := be.Relaxed(t)

	p, err := NewPool(cfg)
	be.NilErr(rt, err)
	defer func() { be.NilErr(rt, p.Close()) }()

	conn0, err := p.AcquireConn()
	be.NilErr(rt, err)

	conn1, err := p.AcquireConn()
	be.NilErr(rt, err)

	var wg sync.WaitGroup
	connChan := make(chan *ServerConn, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc, err := p.AcquireConn()
		be.NilErr(rt, err)
		connChan <- sc
	}()

	time.Sleep(cfg.MaxLifetime + time.Second)

	p.ReleaseConn(conn0)
	wg.Wait()
	p.ReleaseConn(conn1)
	p.ReleaseConn(<-connChan)

	be.Equal(t, 1, p.Stats().OpenConnections)
	be.Equal(t, 1, p.Stats().Idle)
	be.Equal(t, 2, p.Stats().MaxLifetimeClosed)
}

func genBasePoolConfig() PoolConfig {
	return PoolConfig{
		SpawnConn: func(ctx context.Context) (Conner, error) {
			return &connMock{}, nil
		},
		MaxOpen: 1,
		MaxIdle: 1,
	}
}

type connMock struct {
}

func (mc *connMock) Conn() net.Conn {
	return &net.TCPConn{}
}

func (mc *connMock) CheckConn() error {
	return nil
}

func (mc *connMock) CancelRequest(_ context.Context) error {
	return nil
}

func (mc *connMock) Close(_ context.Context) error {
	return nil
}

func (mc *connMock) Exec(_ context.Context, _ string) *pgconn.MultiResultReader {
	return &pgconn.MultiResultReader{}
}
