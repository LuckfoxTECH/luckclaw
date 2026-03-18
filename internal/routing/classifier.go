package routing

// Classifier evaluates features and returns a complexity score in [0, 1].
// Higher score = more complex = use heavy model.
type Classifier interface {
	Score(f Features) float64
}

// RuleClassifier uses structural signals (no API calls, sub-microsecond).
// Weights: attachments 1.0, history-intent 0.70, token>200 0.35, 50-200 0.15,
// code block 0.40, tool calls>3 0.30, tool 1-3 0.15, depth>10 0.20.
type RuleClassifier struct{}

func (c *RuleClassifier) Score(f Features) float64 {
	if f.HasAttachments {
		return 1.0
	}
	var score float64
	if f.AsksForHistory {
		score += 0.70
	}
	switch {
	case f.TokenEstimate > 200:
		score += 0.35
	case f.TokenEstimate > 50:
		score += 0.15
	}
	if f.CodeBlockCount > 0 {
		score += 0.40
	}
	switch {
	case f.RecentToolCalls > 3:
		score += 0.30
	case f.RecentToolCalls > 0:
		score += 0.15
	}
	if f.ConversationDepth > 10 {
		score += 0.20
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}
