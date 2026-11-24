// Package postgres is a discovery.Registry backed by PostgreSQL, so
// instances survive restarts and several Beacon nodes can share them.
//
// Limits stay exact across nodes: Create checks and inserts in one
// transaction that holds a Beacon-wide advisory lock, so concurrent
// registrations, on any node, are counted one after another. Owner checks
// are part of the UPDATE or DELETE they guard. The schema is created and
// migrated by Open.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/heartbeats"
)

// Advisory lock keys. They only have to differ from other users of the
// same database; these spell "beacon" and "beacon migrate".
const (
	createLock  int64 = 0x6265_6163_6f6e
	migrateLock int64 = 0x6265_6163_6f6e_6d
)

// migrations holds the schema changes, applied in file name order.
//
//go:embed migrations/*.sql
var migrations embed.FS

// PostgresRegistry stores instances in PostgreSQL. Use Open to create one,
// and Close it when done. It is safe for concurrent use.
//
// The name repeats the package on purpose, as memory.MemoryRegistry's does.
type PostgresRegistry struct { //nolint:revive // stutter accepted for clarity, as for memory.MemoryRegistry
	pool *pgxpool.Pool
}

var _ discovery.Registry = (*PostgresRegistry)(nil)

// Open connects to the database at url, a PostgreSQL connection string
// such as "postgres://beacon@db:5432/beacon", whose parameters include
// pgx's pool settings such as pool_max_conns. It applies any migrations
// not applied yet, under a lock so that nodes starting together apply each
// once, and fails if the database cannot be reached.
func Open(ctx context.Context, url string) (*PostgresRegistry, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return &PostgresRegistry{pool: pool}, nil
}

// Close closes every connection.
func (registry *PostgresRegistry) Close() {
	registry.pool.Close()
}

// migrate applies the embedded migrations that beacon_schema_migrations
// does not list yet, each in its own transaction, while holding a session
// advisory lock.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLock); err != nil {
		return fmt.Errorf("postgres: migrate: lock: %w", err)
	}

	defer func() {
		// The lock is the session's, so it must be released on the same
		// connection, even if ctx has ended.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLock)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS beacon_schema_migrations (
		version    integer PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}

	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}

	slices.Sort(files)

	for _, file := range files {
		if err := applyMigration(ctx, conn.Conn(), file); err != nil {
			return err
		}
	}

	return nil
}

// applyMigration applies the migration in file unless it is recorded as
// applied. Its version is the number its name starts with.
func applyMigration(ctx context.Context, conn *pgx.Conn, file string) error {
	name := strings.TrimPrefix(file, "migrations/")

	version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
	if err != nil {
		return fmt.Errorf("postgres: migration %s has no version number", name)
	}

	var applied bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM beacon_schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}

	if applied {
		return nil
	}

	statements, err := migrations.ReadFile(file)
	if err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}

	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, string(statements)); err != nil {
			return err
		}

		_, err := tx.Exec(ctx, "INSERT INTO beacon_schema_migrations (version) VALUES ($1)", version)

		return err
	})
	if err != nil {
		return fmt.Errorf("postgres: migration %s: %w", name, err)
	}

	return nil
}

// instanceColumns are the columns scanInstance reads, in its order.
const instanceColumns = "id, name, address, port, owner, registered_at, last_heartbeat"

// scanInstance reads one row of instanceColumns. Timestamps come back with
// microsecond precision, which PostgreSQL stores.
func scanInstance(row pgx.Row) (discovery.Instance, error) {
	var instance discovery.Instance

	err := row.Scan(&instance.ID, &instance.Name, &instance.Address, &instance.Port,
		&instance.Owner, &instance.RegisteredAt, &instance.LastHeartbeat)

	// pgx reads timestamps in the local zone; store and return UTC, as the
	// API does.
	instance.RegisteredAt = instance.RegisteredAt.UTC()
	instance.LastHeartbeat = instance.LastHeartbeat.UTC()

	return instance, err
}

// Create stores instance unless its ID is taken or that would break
// limits, checking and inserting under the Beacon-wide create lock.
func (registry *PostgresRegistry) Create(ctx context.Context, instance discovery.Instance, limits discovery.Limits) error {
	err := pgx.BeginFunc(ctx, registry.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", createLock); err != nil {
			return err
		}

		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM beacon_instances WHERE id = $1)", instance.ID).Scan(&exists); err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: %s", discovery.ErrInstanceExists, instance.ID)
		}

		var total int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM beacon_instances").Scan(&total); err != nil {
			return err
		}

		if total >= int64(limits.Total) {
			return discovery.ErrRegistryFull
		}

		if instance.Owner != "" && limits.PerOwner > 0 {
			var owned int64
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM beacon_instances WHERE owner = $1", instance.Owner).Scan(&owned); err != nil {
				return err
			}

			if owned >= int64(limits.PerOwner) {
				return discovery.ErrOwnerQuotaExceeded
			}
		}

		_, err := tx.Exec(ctx, "INSERT INTO beacon_instances ("+instanceColumns+") VALUES ($1, $2, $3, $4, $5, $6, $7)",
			instance.ID, instance.Name, instance.Address, instance.Port, instance.Owner, instance.RegisteredAt, instance.LastHeartbeat)

		return err
	})
	if err != nil {
		return fmt.Errorf("postgres: create %s: %w", instance.ID, err)
	}

	return nil
}

// Touch sets the LastHeartbeat of the instance with id, if check allows it,
// in one statement.
func (registry *PostgresRegistry) Touch(ctx context.Context, id string, at time.Time, check discovery.OwnerCheck) error {
	tag, err := registry.pool.Exec(ctx,
		"UPDATE beacon_instances SET last_heartbeat = $2 WHERE id = $1 AND ($3 OR owner = '' OR owner = $4)",
		id, at, check.Any, check.Owner)
	if err != nil {
		return fmt.Errorf("postgres: touch %s: %w", id, err)
	}

	if tag.RowsAffected() == 0 {
		return registry.missing(ctx, id)
	}

	return nil
}

// Delete removes the instance with id, if check allows it, in one
// statement.
func (registry *PostgresRegistry) Delete(ctx context.Context, id string, check discovery.OwnerCheck) error {
	tag, err := registry.pool.Exec(ctx,
		"DELETE FROM beacon_instances WHERE id = $1 AND ($2 OR owner = '' OR owner = $3)",
		id, check.Any, check.Owner)
	if err != nil {
		return fmt.Errorf("postgres: delete %s: %w", id, err)
	}

	if tag.RowsAffected() == 0 {
		return registry.missing(ctx, id)
	}

	return nil
}

// missing explains why a guarded change to id affected no row: the
// instance does not exist, or check refused it.
func (registry *PostgresRegistry) missing(ctx context.Context, id string) error {
	var exists bool
	if err := registry.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM beacon_instances WHERE id = $1)", id).Scan(&exists); err != nil {
		return fmt.Errorf("postgres: look up %s: %w", id, err)
	}

	if exists {
		return discovery.ErrInstanceNotOwned
	}

	return discovery.ErrInstanceNotFound
}

// List returns the instances query selects, sorted by ID. The database
// applies the filters and the limit, using the index on name and ID.
func (registry *PostgresRegistry) List(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error) {
	var limit any
	if query.Limit > 0 {
		limit = query.Limit
	}

	rows, err := registry.pool.Query(ctx, "SELECT "+instanceColumns+` FROM beacon_instances
		WHERE ($1 = '' OR name = $1) AND id > $2 AND last_heartbeat >= $3
		ORDER BY id
		LIMIT $4`,
		query.Name, query.After, query.HeartbeatSince, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list: %w", err)
	}

	instances, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (discovery.Instance, error) {
		return scanInstance(row)
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list: %w", err)
	}

	if instances == nil {
		instances = []discovery.Instance{}
	}

	return instances, nil
}

// Get returns the instance with id.
func (registry *PostgresRegistry) Get(ctx context.Context, id string) (discovery.Instance, error) {
	instance, err := scanInstance(registry.pool.QueryRow(ctx, "SELECT "+instanceColumns+" FROM beacon_instances WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return discovery.Instance{}, discovery.ErrInstanceNotFound
	}

	if err != nil {
		return discovery.Instance{}, fmt.Errorf("postgres: get %s: %w", id, err)
	}

	return instance, nil
}

// DeleteStale removes every instance whose last heartbeat is before
// cutoff. Several nodes may run it at once; each instance is removed once.
func (registry *PostgresRegistry) DeleteStale(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := registry.pool.Exec(ctx, "DELETE FROM beacon_instances WHERE last_heartbeat < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres: delete stale: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// TouchMany sets the last heartbeat of every instance in heartbeats in one
// statement, keeping a later one if it holds it and applying each owner
// check, and returns the IDs it did not change: instances that no longer
// exist, or that another owner now holds.
func (registry *PostgresRegistry) TouchMany(ctx context.Context, heartbeats []heartbeats.Heartbeat) ([]string, error) {
	ids := make([]string, 0, len(heartbeats))
	times := make([]time.Time, 0, len(heartbeats))
	owners := make([]string, 0, len(heartbeats))
	anyOwner := make([]bool, 0, len(heartbeats))

	for _, heartbeat := range heartbeats {
		ids = append(ids, heartbeat.ID)
		times = append(times, heartbeat.At)
		owners = append(owners, heartbeat.Check.Owner)
		anyOwner = append(anyOwner, heartbeat.Check.Any)
	}

	rows, err := registry.pool.Query(ctx, `UPDATE beacon_instances AS instance
		SET last_heartbeat = GREATEST(instance.last_heartbeat, heartbeat.at)
		FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::boolean[]) AS heartbeat(id, at, owner, any_owner)
		WHERE instance.id = heartbeat.id
			AND (heartbeat.any_owner OR instance.owner = '' OR instance.owner = heartbeat.owner)
		RETURNING instance.id`, ids, times, owners, anyOwner)
	if err != nil {
		return nil, fmt.Errorf("postgres: touch many: %w", err)
	}

	touched, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("postgres: touch many: %w", err)
	}

	changed := make(map[string]bool, len(touched))
	for _, id := range touched {
		changed[id] = true
	}

	var unchanged []string

	for _, id := range ids {
		if !changed[id] {
			unchanged = append(unchanged, id)
		}
	}

	return unchanged, nil
}
