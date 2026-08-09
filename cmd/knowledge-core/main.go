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
		return errors.New("usage: knowledge-core <serve|health|import|candidate-list|candidate-approval|approval-resolve|candidate-promote|knowledge|query|run> --data-dir <absolute-path> [--json]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dataDir := flags.String("data-dir", "", "absolute knowledge data directory")
	jsonOutput := flags.Bool("json", false, "write a JSON result")
	var content, kind, displayName, idempotencyKey, question, mode, profile, runID, ingestionID, candidateID, approvalID, token, caller *string
	var approve *bool
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
	case "run":
		runID = flags.String("run-id", "", "query run identifier")
	case "candidate-list":
		ingestionID = flags.String("ingestion-id", "", "ingestion identifier")
	case "candidate-approval":
		candidateID = flags.String("candidate-id", "", "candidate identifier")
		caller = flags.String("caller", "cli", "approval caller")
	case "approval-resolve":
		approvalID = flags.String("approval-id", "", "approval identifier")
		approve = flags.Bool("approve", false, "approve the requested action")
		caller = flags.String("caller", "cli", "approval caller")
	case "candidate-promote":
		candidateID = flags.String("candidate-id", "", "candidate identifier")
		token = flags.String("token", "", "approved single-use token")
		caller = flags.String("caller", "cli", "approval caller")
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
	case "knowledge":
		result, err := app.NewKnowledgeService(core).ListKnowledge(context.Background())
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "candidate-list":
		result, err := core.ListCandidates(context.Background(), *ingestionID)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "candidate-approval":
		result, err := app.NewKnowledgeService(core).RequestCandidatePromotion(context.Background(), *candidateID, *caller)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "approval-resolve":
		result, err := app.NewKnowledgeService(core).ResolveApproval(context.Background(), *approvalID, *caller, *approve)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "candidate-promote":
		knowledge, revision, err := app.NewKnowledgeService(core).PromoteCandidate(context.Background(), *candidateID, *token, *caller)
		if err != nil {
			return err
		}
		return writeResult(map[string]any{"knowledge": knowledge, "revision": revision}, *jsonOutput)
	case "query":
		result, err := app.NewQueryService(core).Ask(context.Background(), *question, *mode, *profile)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "run":
		result, err := app.NewQueryService(core).GetRun(context.Background(), *runID)
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
