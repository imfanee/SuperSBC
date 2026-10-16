package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/model"
)

const endpointCols = `id, carrier_id, seq, host, port, transport::text AS transport, priority, weight, enabled, notes, created_at, updated_at`

// Endpoints lists the signalling endpoints of one carrier (D-75).
func (s *Store) Endpoints(ctx context.Context, carrierID uuid.UUID) ([]model.CarrierEndpoint, error) {
	out, err := many[model.CarrierEndpoint](ctx, s.pool,
		`SELECT `+endpointCols+` FROM carrier_endpoints WHERE carrier_id = $1 ORDER BY priority, seq`, carrierID)
	if out == nil {
		out = []model.CarrierEndpoint{}
	}
	return out, err
}

// AllEndpoints returns every endpoint of every live carrier, by carrier id.
func (s *Store) AllEndpoints(ctx context.Context) (map[uuid.UUID][]model.CarrierEndpoint, error) {
	rows, err := many[model.CarrierEndpoint](ctx, s.pool, `SELECT e.id, e.carrier_id, e.seq, e.host, e.port, e.transport::text AS transport,
		e.priority, e.weight, e.enabled, e.notes, e.created_at, e.updated_at
		FROM carrier_endpoints e JOIN carriers c ON c.id = e.carrier_id AND c.deleted_at IS NULL
		ORDER BY e.carrier_id, e.priority, e.seq`)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]model.CarrierEndpoint{}
	for _, e := range rows {
		out[e.CarrierID] = append(out[e.CarrierID], e)
	}
	return out, nil
}

// EndpointSpec is one entry of a replace-all endpoint list.
type EndpointSpec struct {
	Host      string `json:"host" validate:"required,hostname|ip"`
	Port      int    `json:"port"`
	Transport string `json:"transport" validate:"omitempty,oneof=udp tcp tls"`
	Priority  int    `json:"priority"`
	Weight    int    `json:"weight"`
	Enabled   *bool  `json:"enabled"`
	Notes     string `json:"notes" validate:"max=500"`
}

// ReplaceEndpoints replaces the endpoint list of a carrier, keeping the
// sequence numbers of addresses that stay so their gateway (and its health)
// survives an edit. The first endpoint is mirrored into the carrier's legacy
// gateway columns, which the UI and the firewall export still read.
func (s *Store) ReplaceEndpoints(ctx context.Context, carrierID uuid.UUID, specs []EndpointSpec) ([]model.CarrierEndpoint, error) {
	err := s.WithTx(ctx, func(tx Tx) error {
		existing, err := many[model.CarrierEndpoint](ctx, tx, `SELECT `+endpointCols+` FROM carrier_endpoints WHERE carrier_id = $1 FOR UPDATE`, carrierID)
		if err != nil {
			return err
		}
		seqOf := map[string]int{}
		used := map[int]bool{}
		for _, e := range existing {
			seqOf[endpointKey(e.Host, e.Port, e.Transport)] = e.Seq
		}
		if _, err := tx.Exec(ctx, `DELETE FROM carrier_endpoints WHERE carrier_id = $1`, carrierID); err != nil {
			return wrapErr(err)
		}
		next := 1
		for i, sp := range specs {
			port := sp.Port
			if port <= 0 {
				port = 5060
			}
			transport := sp.Transport
			if transport == "" {
				transport = "udp"
			}
			prio := sp.Priority
			if prio <= 0 {
				prio = i + 1
			}
			weight := sp.Weight
			if weight <= 0 { // endpoints always take part; the weight only shapes the share
				weight = 100
			}
			enabled := true
			if sp.Enabled != nil {
				enabled = *sp.Enabled
			}
			seq, ok := seqOf[endpointKey(strings.TrimSpace(sp.Host), port, transport)]
			if !ok || used[seq] {
				for used[next] {
					next++
				}
				seq = next
				used[next] = true
			}
			used[seq] = true
			if _, err := tx.Exec(ctx, `INSERT INTO carrier_endpoints (carrier_id, seq, host, port, transport, priority, weight, enabled, notes)
				VALUES ($1, $2, $3, $4, $5::transport_kind, $6, $7, $8, $9)`,
				carrierID, seq, strings.TrimSpace(sp.Host), port, transport, prio, weight, enabled, sp.Notes); err != nil {
				return wrapErr(err)
			}
		}
		// Mirror the primary endpoint into the carrier row.
		_, err = tx.Exec(ctx, `UPDATE carriers c SET gateway_host = e.host, gateway_port = e.port, transport = e.transport
			FROM (SELECT host, port, transport FROM carrier_endpoints WHERE carrier_id = $1 ORDER BY priority, seq LIMIT 1) e
			WHERE c.id = $1`, carrierID)
		return wrapErr(err)
	})
	if err != nil {
		return nil, err
	}
	return s.Endpoints(ctx, carrierID)
}

// EnsurePrimaryEndpoint keeps a carrier with no endpoint row consistent with
// its gateway columns (a carrier created through the plain carrier API).
func EnsurePrimaryEndpoint(ctx context.Context, q Querier, c *model.Carrier) error {
	_, err := q.Exec(ctx, `INSERT INTO carrier_endpoints (carrier_id, seq, host, port, transport)
		VALUES ($1, 1, $2, $3, $4::transport_kind)
		ON CONFLICT (carrier_id, host, port, transport) DO NOTHING`, c.ID, c.GatewayHost, c.GatewayPort, c.Transport)
	return wrapErr(err)
}

func endpointKey(host string, port int, transport string) string {
	return fmt.Sprintf("%s|%d|%s", host, port, transport)
}
