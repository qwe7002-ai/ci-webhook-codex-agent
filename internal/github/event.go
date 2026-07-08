package github

import (
	"encoding/json"
	"fmt"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
)

// Playbook selects which prompt/flow Codex runs for an incident.
const (
	// PlaybookTriageIssue: evaluate the event and open a GitLab issue (default).
	PlaybookTriageIssue = "triage_issue"
	// PlaybookPRReview: review the GitHub PR (gh) and mirror it to a GitLab MR.
	PlaybookPRReview = "pr_review"
	// PlaybookPRMergeSync: the GitHub PR was merged; merge the mirrored GitLab MR.
	PlaybookPRMergeSync = "pr_merge_sync"
)

// Incident is a normalized view of a GitHub webhook event, decoupled from the
// specific event type so downstream code (prompt building, triage) is uniform.
type Incident struct {
	DeliveryID string            // X-GitHub-Delivery, used for dedup/logging
	EventType  string            // X-GitHub-Event, e.g. "workflow_run"
	Action     string            // payload.action, when present
	Repo       string            // "owner/repo"
	Title      string            // short human title of the event
	Summary    string            // longer body/description text for the model
	URL        string            // link back to the GitHub object
	Actor      string            // login of the triggering user, when known
	Extra      map[string]string // event-specific fields (conclusion, branch, ...)

	Playbook string  // which flow to run (see Playbook* constants)
	PR       *PRInfo // populated for pull_request events
}

// PRInfo carries the pull_request fields the PR playbooks need.
type PRInfo struct {
	Number      int
	State       string
	Merged      bool
	HeadRef     string // source branch
	BaseRef     string // target branch
	HeadRepoURL string // clone URL of the head repo (may be a fork)
	BaseRepoURL string // clone URL of the base repo
	HeadSHA     string
	MergeSHA    string // merge commit SHA once merged
}

// Decision is the result of applying config filters to a raw event.
type Decision struct {
	Process  bool
	Incident Incident
	// Reason explains a skip (empty when Process is true), for logging.
	Reason string
}

// minimal shapes we pull out of each payload; unused fields are ignored.
type repoRef struct {
	FullName string `json:"full_name"`
}
type userRef struct {
	Login string `json:"login"`
}

// Evaluate parses a raw webhook body for the given event type and applies the
// configured filters, returning whether it should be processed and, if so, a
// normalized Incident.
func Evaluate(cfg *config.Config, eventType, deliveryID string, body []byte) (Decision, error) {
	filter, ok := cfg.GitHub.Events[eventType]
	if !ok || !filter.Enabled {
		return Decision{Reason: fmt.Sprintf("event %q not enabled", eventType)}, nil
	}

	// Every interesting payload has "action" except push; decode it generically.
	var head struct {
		Action string  `json:"action"`
		Repo   repoRef `json:"repository"`
		Sender userRef `json:"sender"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return Decision{}, fmt.Errorf("decode payload head: %w", err)
	}

	if !actionAllowed(filter, head.Action) {
		return Decision{Reason: fmt.Sprintf("action %q filtered out", head.Action)}, nil
	}

	inc := Incident{
		DeliveryID: deliveryID,
		EventType:  eventType,
		Action:     head.Action,
		Repo:       head.Repo.FullName,
		Actor:      head.Sender.Login,
		Extra:      map[string]string{},
	}

	// Enrich per event type, and let CI events apply the conclusion filter.
	switch eventType {
	case "issues":
		enrichIssue(body, &inc)
	case "pull_request":
		if reason, ok := enrichPullRequest(body, &inc); !ok {
			return Decision{Reason: reason}, nil
		}
	case "push":
		enrichPush(body, &inc)
	case "workflow_run":
		if reason, ok := enrichWorkflowRun(filter, body, &inc); !ok {
			return Decision{Reason: reason}, nil
		}
	case "check_run":
		if reason, ok := enrichCheckRun(filter, body, &inc); !ok {
			return Decision{Reason: reason}, nil
		}
	default:
		// Unknown-but-enabled event: fall back to a generic summary so the agent
		// still gets something useful rather than silently dropping it.
		inc.Title = fmt.Sprintf("%s event on %s", eventType, inc.Repo)
		inc.Summary = string(body)
	}

	// Everything that isn't a PR playbook defaults to opening a triage issue.
	if inc.Playbook == "" {
		inc.Playbook = PlaybookTriageIssue
	}

	return Decision{Process: true, Incident: inc}, nil
}

func actionAllowed(f config.EventFilter, action string) bool {
	if len(f.Actions) == 0 {
		return true
	}
	for _, a := range f.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func conclusionAllowed(f config.EventFilter, conclusion string) bool {
	if len(f.Conclusions) == 0 {
		return true
	}
	for _, c := range f.Conclusions {
		if c == conclusion {
			return true
		}
	}
	return false
}

func enrichIssue(body []byte, inc *Incident) {
	var p struct {
		Issue struct {
			Number  int     `json:"number"`
			Title   string  `json:"title"`
			Body    string  `json:"body"`
			HTMLURL string  `json:"html_url"`
			User    userRef `json:"user"`
			Labels  []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"issue"`
	}
	_ = json.Unmarshal(body, &p)
	inc.Title = fmt.Sprintf("[GitHub issue #%d] %s", p.Issue.Number, p.Issue.Title)
	inc.Summary = p.Issue.Body
	inc.URL = p.Issue.HTMLURL
	if inc.Actor == "" {
		inc.Actor = p.Issue.User.Login
	}
}

// enrichPullRequest populates PR fields and selects the playbook:
//   - opened / reopened / ready_for_review -> review + mirror to a GitLab MR
//   - closed with merged=true              -> merge the mirrored GitLab MR
//   - closed without merge                 -> skipped
func enrichPullRequest(body []byte, inc *Incident) (string, bool) {
	var p struct {
		Number      int `json:"number"`
		PullRequest struct {
			Title          string `json:"title"`
			Body           string `json:"body"`
			HTMLURL        string `json:"html_url"`
			State          string `json:"state"`
			Merged         bool   `json:"merged"`
			MergeCommitSHA string `json:"merge_commit_sha"`
			Head           struct {
				Ref  string `json:"ref"`
				SHA  string `json:"sha"`
				Repo struct {
					CloneURL string `json:"clone_url"`
				} `json:"repo"`
			} `json:"head"`
			Base struct {
				Ref  string `json:"ref"`
				Repo struct {
					CloneURL string `json:"clone_url"`
				} `json:"repo"`
			} `json:"base"`
		} `json:"pull_request"`
	}
	_ = json.Unmarshal(body, &p)
	pr := p.PullRequest

	inc.Title = fmt.Sprintf("[GitHub PR #%d] %s", p.Number, pr.Title)
	inc.Summary = pr.Body
	inc.URL = pr.HTMLURL
	inc.PR = &PRInfo{
		Number:      p.Number,
		State:       pr.State,
		Merged:      pr.Merged,
		HeadRef:     pr.Head.Ref,
		BaseRef:     pr.Base.Ref,
		HeadRepoURL: pr.Head.Repo.CloneURL,
		BaseRepoURL: pr.Base.Repo.CloneURL,
		HeadSHA:     pr.Head.SHA,
		MergeSHA:    pr.MergeCommitSHA,
	}

	if inc.Action == "closed" {
		if !pr.Merged {
			return fmt.Sprintf("PR #%d closed without merge", p.Number), false
		}
		inc.Playbook = PlaybookPRMergeSync
		return "", true
	}
	// opened / reopened / ready_for_review (whatever the config allows).
	inc.Playbook = PlaybookPRReview
	return "", true
}

func enrichPush(body []byte, inc *Incident) {
	var p struct {
		Ref     string `json:"ref"`
		Compare string `json:"compare"`
		Pusher  struct {
			Name string `json:"name"`
		} `json:"pusher"`
		HeadCommit struct {
			Message string `json:"message"`
		} `json:"head_commit"`
		Commits []struct {
			Message string `json:"message"`
		} `json:"commits"`
	}
	_ = json.Unmarshal(body, &p)
	inc.Title = fmt.Sprintf("[GitHub push] %s -> %s", inc.Repo, p.Ref)
	inc.Summary = p.HeadCommit.Message
	inc.URL = p.Compare
	inc.Extra["ref"] = p.Ref
	inc.Extra["commits"] = fmt.Sprintf("%d", len(p.Commits))
	if inc.Actor == "" {
		inc.Actor = p.Pusher.Name
	}
}

func enrichWorkflowRun(f config.EventFilter, body []byte, inc *Incident) (string, bool) {
	var p struct {
		WorkflowRun struct {
			Name       string `json:"name"`
			HTMLURL    string `json:"html_url"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadBranch string `json:"head_branch"`
			HeadSHA    string `json:"head_sha"`
			Event      string `json:"event"`
		} `json:"workflow_run"`
	}
	_ = json.Unmarshal(body, &p)
	wr := p.WorkflowRun

	if !conclusionAllowed(f, wr.Conclusion) {
		return fmt.Sprintf("workflow conclusion %q filtered out", wr.Conclusion), false
	}
	inc.Title = fmt.Sprintf("[CI failure] workflow %q on %s@%s", wr.Name, inc.Repo, wr.HeadBranch)
	inc.Summary = fmt.Sprintf("Workflow %q finished with status=%s conclusion=%s (triggered by %s event).",
		wr.Name, wr.Status, wr.Conclusion, wr.Event)
	inc.URL = wr.HTMLURL
	inc.Extra["conclusion"] = wr.Conclusion
	inc.Extra["branch"] = wr.HeadBranch
	inc.Extra["sha"] = wr.HeadSHA
	return "", true
}

func enrichCheckRun(f config.EventFilter, body []byte, inc *Incident) (string, bool) {
	var p struct {
		CheckRun struct {
			Name       string `json:"name"`
			HTMLURL    string `json:"html_url"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Output     struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
			} `json:"output"`
		} `json:"check_run"`
	}
	_ = json.Unmarshal(body, &p)
	cr := p.CheckRun

	if !conclusionAllowed(f, cr.Conclusion) {
		return fmt.Sprintf("check_run conclusion %q filtered out", cr.Conclusion), false
	}
	inc.Title = fmt.Sprintf("[CI failure] check %q on %s", cr.Name, inc.Repo)
	inc.Summary = fmt.Sprintf("Check %q status=%s conclusion=%s\n%s\n%s",
		cr.Name, cr.Status, cr.Conclusion, cr.Output.Title, cr.Output.Summary)
	inc.URL = cr.HTMLURL
	inc.Extra["conclusion"] = cr.Conclusion
	return "", true
}
