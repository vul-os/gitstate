package main

// Response structs used for rendering human-readable summaries. In --json mode
// the raw server bytes are streamed unchanged, so these only need to match the
// fields gittrack actually prints. Unknown fields are ignored by encoding/json,
// which keeps gittrack tolerant of the API evolving additional context.

// issue mirrors the /api/issues element and the "issue" sub-object of the
// context bundle (camelCase JSON, matching internal/api issueResponse).
type issue struct {
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	Platform     string   `json:"platform"`
	ExternalID   string   `json:"externalId"`
	Number       int      `json:"number"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	State        string   `json:"state"`
	DerivedState string   `json:"derivedState"`
	Assignee     string   `json:"assignee"`
	Labels       []string `json:"labels"`
}

// relatedPR is a PR linked to an issue inside the context bundle.
type relatedPR struct {
	ID           string `json:"id"`
	Number       int    `json:"number"`
	Title        string `json:"title"`
	State        string `json:"state"`
	Merged       bool   `json:"merged"`
	LeadTimeSecs int64  `json:"leadTimeSecs"`
}

// relatedCommit is a recent commit linked to the issue.
type relatedCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// similarIssue is a historically-similar issue plus its resolving PR.
type similarIssue struct {
	ID           string     `json:"id"`
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	State        string     `json:"state"`
	ResolvingPR  *relatedPR `json:"resolvingPr"`
}

// issueContext is the bundle returned by GET /api/context/issue/{id}.
type issueContext struct {
	Issue        issue           `json:"issue"`
	RelatedPRs   []relatedPR     `json:"relatedPrs"`
	Commits      []relatedCommit `json:"commits"`
	TouchedPaths []string        `json:"touchedPaths"`
	Similar      []similarIssue  `json:"similarIssues"`
}

// agentRun mirrors the agent_runs element returned by POST/GET /api/agent-runs
// (camelCase JSON, matching internal/store.AgentRun). Only the fields gittrack
// prints are listed; unknown fields are ignored.
type agentRun struct {
	ID          string `json:"id"`
	Goal        string `json:"goal"`
	AgentName   string `json:"agentName"`
	Branch      string `json:"branch"`
	HumanAction string `json:"humanAction"`
	TestsPassed *bool  `json:"testsPassed"`
	Iterations  *int   `json:"iterations"`
	DiffSummary struct {
		Additions    int `json:"additions"`
		Deletions    int `json:"deletions"`
		ChangedFiles int `json:"changedFiles"`
	} `json:"diffSummary"`
	CreatedAt string `json:"createdAt"`
}

// prContext is the bundle returned by GET /api/context/pr/{id}. It must mirror
// store.PRContextBundle: the diff shape is a nested object (not a string), the
// effort estimate lives under "estimate", and cycleTimeSecs is omitted when nil.
type prContext struct {
	PR struct {
		ID          string `json:"id"`
		Number      int    `json:"number"`
		Title       string `json:"title"`
		State       string `json:"state"`
		Merged      bool   `json:"merged"`
		AuthorLogin string `json:"authorLogin"`
	} `json:"pr"`
	DiffSummary struct {
		Additions    int `json:"additions"`
		Deletions    int `json:"deletions"`
		ChangedFiles int `json:"changedFiles"`
	} `json:"diffSummary"`
	CycleTimeSecs *int64 `json:"cycleTimeSecs"`
	Estimate      *struct {
		PredictedSecs *float64 `json:"predictedSecs"`
		SizeBucket    string   `json:"sizeBucket"`
		ChangeType    string   `json:"changeType"`
	} `json:"estimate"`
}
