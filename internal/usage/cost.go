package usage

type CostTracker struct {
	InputTokens  int
	OutputTokens int
}

func (c *CostTracker) Add(inputTokens, outputTokens int) {
	c.InputTokens += inputTokens
	c.OutputTokens += outputTokens
}
