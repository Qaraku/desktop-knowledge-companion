package domain

import "time"

type QueryRun struct {
	ID               string       `json:"id"`
	Question         string       `json:"question"`
	Mode             string       `json:"mode"`
	KnowledgeVersion int          `json:"knowledge_version"`
	ProfileVersion   string       `json:"profile_version"`
	State            string       `json:"state"`
	Answer           string       `json:"answer,omitempty"`
	RefusalReason    string       `json:"refusal_reason,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	CompletedAt      time.Time    `json:"completed_at,omitempty"`
	Citations        []Citation   `json:"citations,omitempty"`
	Trace            []TraceEvent `json:"trace,omitempty"`
}

type Citation struct {
	Ordinal     int    `json:"ordinal"`
	Origin      string `json:"origin"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	RevisionID  string `json:"revision_id,omitempty"`
	Excerpt     string `json:"excerpt"`
}

type TraceEvent struct {
	Sequence   int       `json:"sequence"`
	Stage      string    `json:"stage"`
	Payload    string    `json:"payload"`
	OccurredAt time.Time `json:"occurred_at"`
}
