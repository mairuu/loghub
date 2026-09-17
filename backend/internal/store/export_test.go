package store

import "time"

// SetInsertChunk changes the batch size until the returned func is called.
func SetInsertChunk(n int) (restore func()) {
	old := insertChunk
	insertChunk = n
	return func() { insertChunk = old }
}

var Refusal = refusal
var SearchError = searchError

func PickInterval(name string, from, to time.Time) (string, time.Time, int, error) {
	iv, first, n, err := pickInterval(name, from, to)
	return iv.name, first, n, err
}
