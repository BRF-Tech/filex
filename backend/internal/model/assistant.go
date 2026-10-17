package model

// AssistantConversation is one conversation a person had with the AI
// assistant built into the explorer (migration 00126, task #269). The
// conversation itself - its frozen system prompt, its messages, its tool
// steps and the batch that waits for an approval - is State, a JSON document
// the handlers read and write whole (handlers/assistant_state.go); the row
// adds who it belongs to, a title for the list and its times.
type AssistantConversation struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// Key is the random key the row was written with (the portable way to
	// read a new row back); never sent.
	Key   string `json:"-"`
	Title string `json:"title"`
	State string `json:"-"`
	// Rev moves by one with every write (compare-and-set).
	Rev int64 `json:"-"`
	// CreatedMs and UpdatedMs are unix milliseconds.
	CreatedMs int64 `json:"created_ms"`
	UpdatedMs int64 `json:"updated_ms"`
}
