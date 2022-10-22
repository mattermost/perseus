package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type PoolManager struct {
	mut   sync.Mutex
	pools map[string]*Pool

	dsn    string //XXX: This will later be dynamically fetched.
	logger *log.Logger
}

func NewPoolManager(dsn string, logger *log.Logger) *PoolManager {
	return &PoolManager{
		pools:  make(map[string]*Pool),
		dsn:    dsn,
		logger: logger,
	}
}

func (pm *PoolManager) GetOrCreatePool(db, schema string) (pool *Pool, err error) {
	// XXX: Get the target DSN from the source DB and schema name
	// XXX: Possibly factor in read replicas as well.

	// XXX: Check if pool already exists.
	pm.mut.Lock()
	pool = pm.pools["host"+"db"]
	pm.mut.Unlock()
	if pool != nil {
		return pool, nil
	}

	// XXX: verify which dsn gets bound to the function.
	spawnConn := func(ctx context.Context) (*pgconn.PgConn, error) {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		pgConn, err := pgconn.Connect(ctx, pm.dsn)
		if err != nil {
			return nil, fmt.Errorf("pgconn failed to connect: %w", err)
		}

		// We don't hijack the connection here
		// because we do need to use pgConn.Close to gracefully
		// send the Terminate signal to PG. It would be cumbersome
		// to wrap the hijacked connection again just to gracefully close.
		// Instead we trust the code not to misuse the pgconn.
		return pgConn, nil
	}

	pool, err = NewPool(PoolConfig{
		SpawnConn:         spawnConn,
		Logger:            pm.logger,
		MaxIdle:           3,
		MaxOpen:           3,
		MaxLifetime:       time.Hour,
		MaxIdleTime:       5 * time.Minute,
		ConnCreateTimeout: 5 * time.Second,
		ConnCloseTimeout:  time.Second,
	})
	if err != nil {
		return nil, err
	}

	// Place it in the pool
	pm.mut.Lock()
	pm.pools["host"+"db"] = pool
	pm.mut.Unlock()

	return pool, nil
}

// Close closes all pools
func (pm *PoolManager) Close() error {
	pm.mut.Lock()
	defer pm.mut.Unlock()
	var err error
	for _, p := range pm.pools {
		err = p.Close()
	}
	return err
}
