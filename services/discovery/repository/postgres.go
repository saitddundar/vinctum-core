package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresPeerRepository struct {
	db *pgxpool.Pool
}

func NewPostgresPeerRepository(db *pgxpool.Pool) *PostgresPeerRepository {
	return &PostgresPeerRepository{db: db}
}

func (r *PostgresPeerRepository) Upsert(ctx context.Context, p *Peer) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO peers (node_id, addrs, public_key, is_relay, last_seen)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (node_id) DO UPDATE
		 SET addrs = EXCLUDED.addrs,
		     public_key = EXCLUDED.public_key,
		     is_relay = EXCLUDED.is_relay,
		     last_seen = NOW()`,
		p.NodeID, p.Addrs, p.PublicKey, p.IsRelay,
	)
	if err != nil {
		return fmt.Errorf("upserting peer: %w", err)
	}
	return nil
}

func (r *PostgresPeerRepository) Find(ctx context.Context, nodeID string) (*Peer, error) {
	p := &Peer{}
	err := r.db.QueryRow(ctx,
		`SELECT node_id, addrs, public_key, is_relay, last_seen FROM peers WHERE node_id = $1`,
		nodeID,
	).Scan(&p.NodeID, &p.Addrs, &p.PublicKey, &p.IsRelay, &p.LastSeen)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("peer not found: %s", nodeID)
	}
	if err != nil {
		return nil, fmt.Errorf("querying peer: %w", err)
	}

	return p, nil
}

func (r *PostgresPeerRepository) All(ctx context.Context) ([]*Peer, error) {
	rows, err := r.db.Query(ctx,
		`SELECT node_id, addrs, public_key, is_relay, last_seen FROM peers ORDER BY last_seen DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying all peers: %w", err)
	}
	defer rows.Close()

	var peers []*Peer
	for rows.Next() {
		p := &Peer{}
		if err := rows.Scan(&p.NodeID, &p.Addrs, &p.PublicKey, &p.IsRelay, &p.LastSeen); err != nil {
			return nil, fmt.Errorf("scanning peer row: %w", err)
		}
		peers = append(peers, p)
	}

	return peers, rows.Err()
}
