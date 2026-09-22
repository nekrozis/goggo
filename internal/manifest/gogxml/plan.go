package gogxml

// SplitChunks divides a file of totalSize into slices of chunkSize.
// Both totalSize and chunkSize are in bytes.
// Returns an empty slice for totalSize == 0.
//
// The count and the closing bound are computed without any addition that can
// overflow int64 (the naive ceil `(t+c-1)/c` overflows for large pairs), and the
// last chunk closes at totalSize-1 by subtraction, which cannot. The returned
// slice scales with the chunk count: callers bound that through the chunk size
// (the CLI accepts 1..1024 MiB), not this function.
func SplitChunks(totalSize int64, chunkSize int64) ([]ChunkPlan, error) {
	if totalSize < 0 {
		return nil, ErrNegativeSize
	}
	if chunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}
	if totalSize == 0 {
		return nil, nil
	}

	numChunks := 1 + int((totalSize-1)/chunkSize)
	plans := make([]ChunkPlan, numChunks)
	for i := 0; i < numChunks; i++ {
		from := int64(i) * chunkSize
		// The closing bound is decided by subtraction: the last chunk ends at
		// totalSize-1, every other one at from+chunkSize-1 — and that addition
		// is only reached when it cannot pass totalSize-1, so it cannot
		// overflow.
		to := totalSize - 1
		if totalSize-from >= chunkSize {
			to = from + chunkSize - 1
		}
		plans[i] = ChunkPlan{
			ID:   i,
			From: from,
			To:   to,
		}
	}
	return plans, nil
}
