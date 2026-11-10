//go:build setupe2e

// Command setupworld builds one fake machine for a shape and serves the
// real `lore setup web` in it until SIGTERM/SIGINT (setupe2e contract).
//
//	go run -tags setupe2e ./tests/setupe2e/cmd/setupworld up --shape '<json>' --dir <empty dir> --port <port> [--lore <bin>] [--invite <email>]
//
// Prints ONE JSON line (setupe2e.Info) on stdout once the server is up;
// exits non-zero with the reason on stderr when anything fails to start.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/thesatellite-ai/lore/tests/setupe2e"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "setupworld:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] != "up" {
		return fmt.Errorf("usage: setupworld up --shape '<json>' --dir <empty dir> --port <port> [--lore <bin>]")
	}
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	shapeJSON := fs.String("shape", "", "the shape JSON (every field required)")
	dir := fs.String("dir", "", "an empty (or missing) folder for the world")
	port := fs.Int("port", 0, "port for lore setup web (0 = a free one)")
	lore := fs.String("lore", "", "a prebuilt lore binary (default: build ./cmd/lore into <dir>/bin)")
	invite := fs.String("invite", "", "make the hub team on this machine and an invite line for this email (Info.invite)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *shapeJSON == "" || *dir == "" {
		return fmt.Errorf("--shape and --dir are required")
	}
	shape, err := setupe2e.ParseShape(*shapeJSON)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	bin := *lore
	if bin == "" {
		abs, err := filepath.Abs(*dir)
		if err != nil {
			return err
		}
		if bin, err = setupe2e.BuildLore(ctx, filepath.Join(abs, "bin")); err != nil {
			return err
		}
	} else if bin, err = filepath.Abs(bin); err != nil {
		return err
	}
	defer setupe2e.CleanupKeys()
	w, err := setupe2e.Build(ctx, *dir, shape, bin)
	if err != nil {
		return err
	}
	if err := w.Start(ctx, *port, bin); err != nil {
		return err
	}
	defer w.Stop()
	if *invite != "" {
		if _, err := w.Invite(ctx, bin, *invite); err != nil {
			return err
		}
	}
	info, err := json.Marshal(w.Info())
	if err != nil {
		return err
	}
	if err := w.WriteInfo(filepath.Join(w.Dir, "world.json")); err != nil {
		return err
	}
	fmt.Println(string(info))
	<-ctx.Done()
	return nil
}
