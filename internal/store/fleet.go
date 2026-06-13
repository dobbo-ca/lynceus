package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Cluster is one logical grouping of instances (a primary + its replicas).
type Cluster struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Instance is one Postgres endpoint within a cluster. Role is populated by the
// primary/replica consolidation bead (ly-99s.3); it defaults to "unknown".
type Instance struct {
	ID        string
	ClusterID string
	Name      string
	Role      string // primary | replica | unknown
	CreatedAt time.Time
}

// ServerStream is the per-stream "monitored database" row (the reused servers
// table). ServerID is the stats-store stream key. InstanceID / DatabaseName are
// "" when not yet linked/known.
type ServerStream struct {
	ServerID     string
	Name         string
	InstanceID   string
	DatabaseName string
	T2Enabled    bool
	CreatedAt    time.Time
}

// CreateCluster inserts a cluster with a generated id and returns it.
func (c *Config) CreateCluster(ctx context.Context, name string) (Cluster, error) {
	cl := Cluster{ID: uuid.NewString(), Name: name}
	err := c.pool.QueryRow(ctx,
		`INSERT INTO cluster (id, name) VALUES ($1, $2) RETURNING created_at`,
		cl.ID, cl.Name,
	).Scan(&cl.CreatedAt)
	return cl, err
}

// CreateInstance inserts an instance under clusterID with a generated id and
// returns it (with the DB-defaulted role).
func (c *Config) CreateInstance(ctx context.Context, clusterID, name string) (Instance, error) {
	in := Instance{ID: uuid.NewString(), ClusterID: clusterID, Name: name}
	err := c.pool.QueryRow(ctx,
		`INSERT INTO instance (id, cluster_id, name) VALUES ($1, $2, $3)
		 RETURNING role, created_at`,
		in.ID, in.ClusterID, in.Name,
	).Scan(&in.Role, &in.CreatedAt)
	return in, err
}

// AssignServerToInstance links a server stream to an instance.
func (c *Config) AssignServerToInstance(ctx context.Context, serverID, instanceID string) error {
	_, err := c.pool.Exec(ctx,
		`UPDATE servers SET instance_id = $2 WHERE id = $1`, serverID, instanceID)
	return err
}

// ListClusters returns all clusters ordered by name.
func (c *Config) ListClusters(ctx context.Context) ([]Cluster, error) {
	rows, err := c.ro.Query(ctx, `SELECT id, name, created_at FROM cluster ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cluster
	for rows.Next() {
		var cl Cluster
		if err := rows.Scan(&cl.ID, &cl.Name, &cl.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, cl)
	}
	return out, rows.Err()
}

// ListInstances returns the instances under clusterID ordered by name.
func (c *Config) ListInstances(ctx context.Context, clusterID string) ([]Instance, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT id, cluster_id, name, role, created_at FROM instance
		  WHERE cluster_id = $1 ORDER BY name, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.ClusterID, &in.Name, &in.Role, &in.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// serverStreamCols is the shared projection for ServerStream scans. instance_id
// and database_name are nullable in the schema → COALESCE to "".
const serverStreamCols = `id, name, COALESCE(instance_id, ''), COALESCE(database_name, ''), t2_enabled, created_at`

func scanServerStream(row pgx.Row) (ServerStream, error) {
	var s ServerStream
	err := row.Scan(&s.ServerID, &s.Name, &s.InstanceID, &s.DatabaseName, &s.T2Enabled, &s.CreatedAt)
	return s, err
}

// ListServerStreams returns the server streams (monitored databases) under
// instanceID ordered by id.
func (c *Config) ListServerStreams(ctx context.Context, instanceID string) ([]ServerStream, error) {
	rows, err := c.ro.Query(ctx,
		`SELECT `+serverStreamCols+` FROM servers WHERE instance_id = $1 ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerStream
	for rows.Next() {
		s, err := scanServerStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ResolveServer returns the stream row plus its parent instance and cluster.
// The inner joins require a linked instance, so an unknown serverID and a
// server with a NULL instance_id (not yet assigned) both surface as
// pgx.ErrNoRows; callers that need to tell those apart must query servers first.
func (c *Config) ResolveServer(ctx context.Context, serverID string) (ServerStream, Instance, Cluster, error) {
	var (
		s  ServerStream
		in Instance
		cl Cluster
	)
	err := c.ro.QueryRow(ctx,
		`SELECT s.id, s.name, COALESCE(s.instance_id, ''), COALESCE(s.database_name, ''), s.t2_enabled, s.created_at,
		        i.id, i.cluster_id, i.name, i.role, i.created_at,
		        c.id, c.name, c.created_at
		   FROM servers s
		   JOIN instance i ON i.id = s.instance_id
		   JOIN cluster  c ON c.id = i.cluster_id
		  WHERE s.id = $1`, serverID,
	).Scan(
		&s.ServerID, &s.Name, &s.InstanceID, &s.DatabaseName, &s.T2Enabled, &s.CreatedAt,
		&in.ID, &in.ClusterID, &in.Name, &in.Role, &in.CreatedAt,
		&cl.ID, &cl.Name, &cl.CreatedAt,
	)
	return s, in, cl, err
}

// ServerIDsForInstance returns the server_id stream keys under instanceID — the
// set to read from the (unchanged) stats store to roll up an instance.
func (c *Config) ServerIDsForInstance(ctx context.Context, instanceID string) ([]string, error) {
	return c.scanServerIDs(ctx,
		`SELECT id FROM servers WHERE instance_id = $1 ORDER BY id`, instanceID)
}

// ServerIDsForCluster returns the server_id stream keys across every instance in
// clusterID — the set to read from the stats store to roll up a cluster.
func (c *Config) ServerIDsForCluster(ctx context.Context, clusterID string) ([]string, error) {
	return c.scanServerIDs(ctx,
		`SELECT s.id FROM servers s
		   JOIN instance i ON i.id = s.instance_id
		  WHERE i.cluster_id = $1 ORDER BY s.id`, clusterID)
}

func (c *Config) scanServerIDs(ctx context.Context, q string, arg string) ([]string, error) {
	rows, err := c.ro.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BackfillFleet links every server stream that has no instance yet to a freshly
// created 1:1 cluster + instance (name derived from the stream). Existing
// single-stream deployments become a cluster-of-one / instance-of-one with no
// behavior change. Idempotent: only NULL-instance_id rows are processed, so a
// re-run creates nothing. Intended to run alongside ApplyConfigMigrations.
func (c *Config) BackfillFleet(ctx context.Context) error {
	rows, err := c.pool.Query(ctx,
		`SELECT id, name FROM servers WHERE instance_id IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	type pending struct{ id, name string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range todo {
		name := p.name
		if name == "" {
			name = p.id
		}
		cl, err := c.CreateCluster(ctx, name)
		if err != nil {
			return err
		}
		in, err := c.CreateInstance(ctx, cl.ID, name)
		if err != nil {
			return err
		}
		if err := c.AssignServerToInstance(ctx, p.id, in.ID); err != nil {
			return err
		}
	}
	return nil
}
