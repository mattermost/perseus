package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"

	"github.com/agnivade/perseus/config"
	"github.com/agnivade/perseus/internal/server"
)

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "config/config.json", "Configuration file for the Perseus service.")
	flag.Parse()

	cfg, err := config.Parse(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not parse config file: %s\n", err)
		os.Exit(1)
	}

	s, err := server.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the server: %s\n", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var stopped atomic.Bool

	go func() {
		err := s.AcceptConns()
		// No need to print error if server is manually stopping.
		if err != nil && !stopped.Load() {
			fmt.Println(err)
		}
	}()
	defer s.Stop()

	<-ctx.Done()
	stopped.Store(true)
}
