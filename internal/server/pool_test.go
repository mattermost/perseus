package server

import (
	"context"
	"log"
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
	be.NilErr(rt, p.Close())
}

func genBasePoolConfig() PoolConfig {
	return PoolConfig{
		SpawnConn: func(ctx context.Context) (Conner, error) {
			return &connMock{}, nil
		},
		Logger:  log.Default(),
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
