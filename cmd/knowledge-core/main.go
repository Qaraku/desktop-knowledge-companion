package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"desktop-knowledge-companion/internal/app"
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
		return errors.New("usage: knowledge-core <serve|health|import|query> --data-dir <absolute-path> [--json]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dataDir := flags.String("data-dir", "", "absolute knowledge data directory")
	jsonOutput := flags.Bool("json", false, "write a JSON result")
	var content, kind, displayName, idempotencyKey, question, mode, profile *string
	switch command {
	case "import":
		content = flags.String("content", "", "text or Markdown content")
		kind = flags.String("kind", "text", "text or markdown")
		displayName = flags.String("display-name", "", "source display name")
		idempotencyKey = flags.String("idempotency-key", "", "stable write idempotency key")
	case "query":
		question = flags.String("question", "", "question to answer")
		mode = flags.String("mode", "strict", "strict, augment, or clarify")
		profile = flags.String("profile-version", "local_v1", "query profile version")
	}
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
		return transport.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr, transport.NewServer(core))
	case "health":
		if *jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(core.Health())
		}
		health := core.Health()
		_, err := fmt.Fprintf(os.Stdout, "ready=%t core=%s schema=%d data_dir=%s\n", health.Ready, health.CoreVersion, health.SchemaVersion, health.DataDir)
		return err
	case "import":
		result, err := app.NewKnowledgeService(core).Import(context.Background(), *kind, *content, *displayName, *idempotencyKey)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "query":
		result, err := app.NewQueryService(core).Ask(context.Background(), *question, *mode, *profile)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func writeResult(value any, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(value)
	}
	_, err := fmt.Fprintln(os.Stdout, value)
	return err
}
