package postgres

import "context"

const deleteBatchSize int32 = 5000

func deleteInBatches(ctx context.Context, deleteBatch func(limit int32) (int64, error)) (int64, error) {
	var total int64
	for {
		n, err := deleteBatch(deleteBatchSize)
		total += n
		if err != nil {
			return total, mapError(err)
		}
		if n < int64(deleteBatchSize) || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}
