package store

// SetInsertChunk changes the batch size until the returned func is called.
func SetInsertChunk(n int) (restore func()) {
	old := insertChunk
	insertChunk = n
	return func() { insertChunk = old }
}

var Refusal = refusal
