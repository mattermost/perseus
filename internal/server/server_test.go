package server

import (
	"database/sql"
	"testing"

	"github.com/carlmjohnson/be"
	_ "github.com/lib/pq"
	"github.com/mattermost/perseus/config"
)

func TestServerConnectionCount(t *testing.T) {
	rt := be.Relaxed(t)

	cfg := config.Config{
		ListenAddress:  "",
		MetricsAddress: "",
		AuthDBSettings: config.AuthDBSettings{
			AuthDBDSN: "",
		},
		PoolSettings: config.PoolSettings{},
	}

	s, err := New(cfg)
	be.NilErr(rt, err)

	go s.AcceptConns()

	db1, err := sql.Open("postgres", "<dsn>")
	be.NilErr(rt, err)

	db2, err := sql.Open("postgres", "dsn")
	be.NilErr(rt, err)

	err = db1.Ping()
	be.NilErr(rt, err)

	err = db1.Ping() // conn already established and had no effect on connection count
	be.NilErr(rt, err)

	err = db2.Ping()
	be.NilErr(rt, err)

	be.Equal(rt, 2, s.numConnectedClients)
}
