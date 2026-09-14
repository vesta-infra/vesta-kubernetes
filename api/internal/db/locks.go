package db

import (
	"context"
	"database/sql"
	"log"
)

// Advisory locks, for work that should run on one API replica at a time.
//
// Background workers here poll: the scheduled-deployment worker, the update checker, and the
// inactivity sweeper. With api.replicas > 1 each of those runs everywhere, which for the
// sweeper means every replica querying Prometheus for every app on every tick. A Postgres
// advisory lock is the cheapest coordination available to processes that already share a
// database and nothing else.
//
// Deliberately try-and-skip rather than wait-and-run. A replica that cannot get the lock has
// nothing useful to do with it -- another replica is already doing that work, and queueing
// would just do it twice in succession.
const (
	// AdvisoryLockSleepSweeper guards the inactivity sweep.
	AdvisoryLockSleepSweeper int64 = 8_100_001
	// AdvisoryLockCostSampler guards cost sampling. Two replicas sampling would double
	// every figure on the page, which is worse than missing a sample: the numbers would be
	// wrong rather than incomplete.
	AdvisoryLockCostSampler int64 = 8_100_002
)

// TryAdvisoryLock takes a session-scoped advisory lock without blocking.
//
// The returned release must be called when the work is done. It is a no-op when the lock was
// not acquired, so callers can defer it unconditionally.
func (d *DB) TryAdvisoryLock(ctx context.Context, key int64) (bool, func(), error) {
	noop := func() {}

	// A dedicated connection, because advisory locks are held by a session and the pool
	// would otherwise release the lock on a different connection than it was taken on.
	conn, err := d.Conn(ctx)
	if err != nil {
		return false, noop, err
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		conn.Close()
		return false, noop, err
	}
	if !acquired {
		conn.Close()
		return false, noop, nil
	}

	return true, func() {
		// Unlock on the same connection, then return it to the pool. Closing without
		// unlocking would also release it, but only once the session actually ends, which
		// a pooled connection may not do for some time.
		if _, err := conn.ExecContext(context.WithoutCancel(ctx),
			`SELECT pg_advisory_unlock($1)`, key); err != nil && err != sql.ErrConnDone {
			log.Printf("[db] releasing advisory lock %d: %v", key, err)
		}
		conn.Close()
	}, nil
}
