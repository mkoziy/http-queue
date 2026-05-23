// Package queue implements the job and worker storage layer over BadgerDB.
package queue

// maintenanceBatchSize is the maximum number of individual maintenance
// operations (e.g., job re-queues, index fixups, worker cleanups) to perform
// in a single BadgerDB write transaction. This prevents unbounded writes
// during worker deregistration and sweep operations when large backlogs exist.
const maintenanceBatchSize = 1000

// batchSlice splits items into chunks of at most batchSize elements each.
// If batchSize <= 0, maintenanceBatchSize is used as the default.
// This helper avoids duplicating batching logic across maintenance paths.
func batchSlice[T any](items []T, batchSize int) [][]T {
	if batchSize <= 0 {
		batchSize = maintenanceBatchSize
	}
	var batches [][]T
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}
	return batches
}
