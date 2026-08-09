package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"desktop-knowledge-companion/internal/agent"
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
		return errors.New("usage: knowledge-core <serve|health|state-snapshot|import|candidate-list|candidate-pending|candidate-update|candidate-reject|candidate-approval|approval-resolve|candidate-promote|knowledge|knowledge-revise|agent-tool-inspect|agent-tool-request-approval|agent-tool-consume-approval|agent-prompt-suggest|agent-prompt-preference-set|agent-pending-list|agent-pending-resolve|query|run> --data-dir <absolute-path> [--json]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dataDir := flags.String("data-dir", "", "absolute knowledge data directory")
	jsonOutput := flags.Bool("json", false, "write a JSON result")
	var content, kind, displayName, idempotencyKey, question, mode, profile, runID, ingestionID, candidateID, approvalID, token, caller, knowledgeID, expectedRevisionID, reason, toolName, parameters, topic, detail, preferenceState, deferredUntil *string
	var expectedVersion *int
	var approve *bool
	switch command {
	case "state-snapshot":
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
	case "candidate-pending":
	case "candidate-update":
		candidateID = flags.String("candidate-id", "", "candidate identifier")
		expectedVersion = flags.Int("expected-version", 0, "candidate version to update")
		content = flags.String("content", "", "replacement candidate content")
	case "candidate-reject":
		candidateID = flags.String("candidate-id", "", "candidate identifier")
		expectedVersion = flags.Int("expected-version", 0, "candidate version to reject")
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
	case "knowledge-revise":
		knowledgeID = flags.String("knowledge-id", "", "knowledge identifier")
		expectedRevisionID = flags.String("expected-revision-id", "", "current revision identifier")
		content = flags.String("content", "", "replacement knowledge content")
		reason = flags.String("reason", "", "typo, format, entry_error, opinion_change, fact_update, time_change, or correction")
	case "agent-tool-inspect":
		toolName = flags.String("tool-name", "", "registered Agent tool name")
	case "agent-tool-request-approval":
		toolName = flags.String("tool-name", "", "registered high-risk Agent tool name")
		parameters = flags.String("parameters", "{}", "JSON tool parameters")
		caller = flags.String("caller", "cli", "approval caller")
	case "agent-tool-consume-approval":
		toolName = flags.String("tool-name", "", "registered high-risk Agent tool name")
		parameters = flags.String("parameters", "{}", "JSON tool parameters")
		token = flags.String("token", "", "approved single-use token")
		caller = flags.String("caller", "cli", "approval caller")
	case "agent-prompt-suggest":
		topic = flags.String("topic", "", "prompt topic")
		detail = flags.String("detail", "", "prompt detail")
	case "agent-prompt-preference-set":
		topic = flags.String("topic", "", "prompt topic")
		preferenceState = flags.String("state", "", "ignored, deferred, or closed")
		deferredUntil = flags.String("deferred-until", "", "RFC3339 time for deferred state")
	case "agent-pending-list":
	case "agent-pending-resolve":
		approvalID = flags.String("id", "", "pending item identifier")
		preferenceState = flags.String("state", "", "ignored, deferred, or closed")
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
	case "state-snapshot":
		result, err := transport.NewServer(core).StateSnapshot(context.Background())
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
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
	case "candidate-pending":
		result, err := app.NewKnowledgeService(core).ListPendingCandidates(context.Background())
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "candidate-update":
		result, err := app.NewKnowledgeService(core).EditCandidate(context.Background(), *candidateID, *expectedVersion, *content)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "candidate-reject":
		result, err := app.NewKnowledgeService(core).RejectCandidate(context.Background(), *candidateID, *expectedVersion)
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
	case "knowledge-revise":
		result, err := app.NewKnowledgeService(core).ReviseKnowledge(context.Background(), *knowledgeID, *expectedRevisionID, *content, *reason)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "agent-tool-inspect":
		return writeResult(agent.NewService(agent.DefaultRegistry(), core).InspectTool(*toolName), *jsonOutput)
	case "agent-tool-request-approval":
		result, err := agent.NewService(agent.DefaultRegistry(), core).RequestHighRiskApproval(context.Background(), *toolName, *caller, *parameters)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "agent-tool-consume-approval":
		if err := agent.NewService(agent.DefaultRegistry(), core).ConsumeHighRiskApproval(context.Background(), *toolName, *caller, *parameters, *token); err != nil {
			return err
		}
		return writeResult(map[string]bool{"authorized": true}, *jsonOutput)
	case "agent-prompt-suggest":
		result, err := agent.NewService(agent.DefaultRegistry(), core).SuggestPrompt(context.Background(), *topic, *detail)
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "agent-prompt-preference-set":
		var deferred *time.Time
		if *deferredUntil != "" {
			value, err := time.Parse(time.RFC3339Nano, *deferredUntil)
			if err != nil {
				return fmt.Errorf("parse deferred-until: %w", err)
			}
			deferred = &value
		}
		if err := agent.NewService(agent.DefaultRegistry(), core).SetPromptPreference(context.Background(), *topic, *preferenceState, deferred); err != nil {
			return err
		}
		return writeResult(map[string]bool{"saved": true}, *jsonOutput)
	case "agent-pending-list":
		result, err := agent.NewService(agent.DefaultRegistry(), core).ListPendingPrompts(context.Background())
		if err != nil {
			return err
		}
		return writeResult(result, *jsonOutput)
	case "agent-pending-resolve":
		if err := agent.NewService(agent.DefaultRegistry(), core).ResolvePendingPrompt(context.Background(), *approvalID, *preferenceState); err != nil {
			return err
		}
		return writeResult(map[string]bool{"resolved": true}, *jsonOutput)
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
