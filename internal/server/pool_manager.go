package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/agnivade/perseus/config"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/kms/kmsiface"

	"github.com/jackc/pgx/v5/pgconn"
)

type PoolManager struct {
	mut   sync.RWMutex
	pools map[string]*Pool

	cfg    config.Config
	logger *log.Logger
	kms    kmsiface.KMSAPI
}

func NewPoolManager(cfg config.Config, logger *log.Logger) (*PoolManager, error) {
	creds := credentials.NewStaticCredentials(cfg.AWSSettings.AccessKeyId, cfg.AWSSettings.SecretAccessKey, "")

	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(cfg.AWSSettings.Region),
		Endpoint:    aws.String(cfg.AWSSettings.Endpoint),
		Credentials: creds,
	})
	if err != nil {
		return nil, fmt.Errorf("error initializing AWS session: %w", err)
	}

	svc := kms.New(sess)

	return &PoolManager{
		pools:  make(map[string]*Pool),
		cfg:    cfg,
		logger: logger,
		kms:    svc,
	}, nil
}

func (pm *PoolManager) GetOrCreatePool(row AuthRow) (pool *Pool, err error) {
	// Fast path once the pool is created
	pm.mut.RLock()
	pool = pm.pools[row.dest_host+row.dest_db]
	pm.mut.RUnlock()
	if pool != nil {
		return pool, nil
	}

	decPass, err := base64.StdEncoding.DecodeString(row.dest_pass_enc)
	if err != nil {
		return nil, fmt.Errorf("error decoding from base64: %w", err)
	}

	dec, err := pm.kms.Decrypt(&kms.DecryptInput{
		CiphertextBlob: decPass,
		KeyId:          aws.String(pm.cfg.AWSSettings.KMSKeyARN),
	})
	if err != nil {
		return nil, fmt.Errorf("error decrypting pass: %w", err)
	}
	row.dest_pass_enc = string(dec.Plaintext)

	spawnConn := func(ctx context.Context) (Conner, error) {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, time.Second*time.Duration(pm.cfg.PoolSettings.ConnCreateTimeoutSecs))
		defer cancel()
		pgConn, err := pgconn.Connect(ctx, createDSN(row))
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
		MaxIdle:           pm.cfg.PoolSettings.MaxIdle,
		MaxOpen:           pm.cfg.PoolSettings.MaxOpen,
		MaxLifetime:       time.Second * time.Duration(pm.cfg.PoolSettings.MaxLifetimeSecs),
		MaxIdleTime:       time.Second * time.Duration(pm.cfg.PoolSettings.MaxIdletimeSecs),
		ConnCreateTimeout: time.Second * time.Duration(pm.cfg.PoolSettings.ConnCreateTimeoutSecs),
		ConnCloseTimeout:  time.Second * time.Duration(pm.cfg.PoolSettings.ConnCloseTimeoutSecs),
		SchemaExecTimeout: time.Second * time.Duration(pm.cfg.PoolSettings.SchemaExecTimeoutSecs),
	})
	if err != nil {
		return nil, err
	}

	// Place it in the map
	pm.mut.Lock()
	pm.pools[row.dest_host+row.dest_db] = pool
	pm.mut.Unlock()

	return pool, nil
}

func (pm *PoolManager) Reload(cfg config.Config) {
	pm.mut.RLock()
	defer pm.mut.RUnlock()
	for _, p := range pm.pools {
		p.Reload(PoolConfig{
			MaxIdle:           cfg.PoolSettings.MaxIdle,
			MaxOpen:           cfg.PoolSettings.MaxOpen,
			MaxLifetime:       time.Second * time.Duration(cfg.PoolSettings.MaxLifetimeSecs),
			MaxIdleTime:       time.Second * time.Duration(cfg.PoolSettings.MaxIdletimeSecs),
			ConnCreateTimeout: time.Second * time.Duration(cfg.PoolSettings.ConnCreateTimeoutSecs),
			ConnCloseTimeout:  time.Second * time.Duration(cfg.PoolSettings.ConnCloseTimeoutSecs),
			SchemaExecTimeout: time.Second * time.Duration(cfg.PoolSettings.SchemaExecTimeoutSecs),
		})
	}
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

func createDSN(row AuthRow) string {
	// postgres://mmuser:mostest@localhost:5433/loadtest?sslmode=disable
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", row.dest_user, row.dest_pass_enc, row.dest_host, row.dest_db)
}
