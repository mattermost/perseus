package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/agnivade/perseus"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	addr := flag.String("addr", ":5432", "postgres protocol bind address")
	flag.Parse()

	log.SetFlags(log.Lshortfile|log.LstdFlags)


	s := perseus.NewServer(*addr)
	if err := s.Start(); err != nil {
		return err
	}

	log.Printf("listening on %s", *addr)

	// Wait on signal before shutting down.
	<-ctx.Done()
	log.Printf("SIGINT received, shutting down")

	if err := s.Stop(); err != nil {
		return err
	}

	return nil
}
