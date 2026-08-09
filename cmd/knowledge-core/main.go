package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"desktop-knowledge-companion/internal/store"
	"desktop-knowledge-companion/internal/transport"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: knowledge-core <serve|health> --data-dir <absolute-path> [--json]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dataDir := flags.String("data-dir", "", "absolute knowledge data directory")
	jsonOutput := flags.Bool("json", false, "write a JSON result")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	core, err := store.Open(context.Background(), *dataDir)
	if err != nil {
		return err
	}
	defer core.Close()

	switch command {
	case "serve":
		return transport.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr, core)
	case "health":
		if *jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(core.Health())
		}
		health := core.Health()
		_, err := fmt.Fprintf(os.Stdout, "ready=%t core=%s schema=%d data_dir=%s\n", health.Ready, health.CoreVersion, health.SchemaVersion, health.DataDir)
		return err
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}
