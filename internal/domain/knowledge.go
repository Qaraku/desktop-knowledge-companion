package domain

import "time"

type CandidateState string

const (
	CandidateProposed   CandidateState = "proposed"
	CandidateEditing    CandidateState = "editing"
	CandidateRejected   CandidateState = "rejected"
	CandidateSuperseded CandidateState = "superseded"
	CandidatePromoted   CandidateState = "promoted"
)

type CandidateVersion struct {
	ID              string `json:"id"`
	ExpectedVersion int    `json:"expected_version"`
}

type Candidate struct {
	ID                  string         `json:"id"`
	IngestionID         string         `json:"ingestion_id"`
	Ordinal             int            `json:"ordinal"`
	Version             int            `json:"version"`
	Content             string         `json:"content"`
	TitlePath           []string       `json:"title_path"`
	State               CandidateState `json:"state"`
	PromotedKnowledgeID string         `json:"promoted_knowledge_id,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type SourceDocument struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Content     string    `json:"content"`
	DisplayName string    `json:"display_name,omitempty"`
	InputAt     time.Time `json:"input_at"`
}

type Knowledge struct {
	ID                string    `json:"id"`
	State             string    `json:"state"`
	CurrentRevisionID string    `json:"current_revision_id"`
	CreatedAt         time.Time `json:"created_at"`
}

type Revision struct {
	ID               string    `json:"id"`
	KnowledgeID      string    `json:"knowledge_id"`
	ParentRevisionID string    `json:"parent_revision_id,omitempty"`
	Content          string    `json:"content"`
	Reason           string    `json:"reason"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"created_at"`
}

type KnowledgeRelation struct {
	ID              string    `json:"id"`
	FromKnowledgeID string    `json:"from_knowledge_id"`
	ToKnowledgeID   string    `json:"to_knowledge_id"`
	Kind            string    `json:"kind"`
	CreatedAt       time.Time `json:"created_at"`
}
