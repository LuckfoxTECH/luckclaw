package routing

const defaultThreshold = 0.35

// Router selects light vs heavy model by message complexity.
type Router struct {
	cfg        RouterConfig
	classifier Classifier
}

// RouterConfig holds routing settings.
type RouterConfig struct {
	LightModel string
	Threshold  float64
}

// NewRouter creates a Router. If threshold <= 0, uses defaultThreshold.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaultThreshold
	}
	return &Router{
		cfg:        cfg,
		classifier: &RuleClassifier{},
	}
}

// SelectModel returns (model, usedLight, score).
// If score < threshold: use LightModel; else use primaryModel.
func (r *Router) SelectModel(msg string, history []map[string]any, primaryModel string) (model string, usedLight bool, score float64) {
	features := ExtractFeatures(msg, history)
	score = r.classifier.Score(features)
	if score < r.cfg.Threshold {
		return r.cfg.LightModel, true, score
	}
	return primaryModel, false, score
}
