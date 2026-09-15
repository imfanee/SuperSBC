package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

// RouteGroupByName loads a route group by name.
func (s *Store) RouteGroupByName(ctx context.Context, name string) (*model.RouteGroup, error) {
	return one[model.RouteGroup](ctx, s.pool, `SELECT id, name, description, lcr_mode, created_at, updated_at FROM route_groups WHERE name = $1 AND deleted_at IS NULL`, name)
}

// RouteGroupByID loads a route group.
func (s *Store) RouteGroupByID(ctx context.Context, id uuid.UUID) (*model.RouteGroup, error) {
	return one[model.RouteGroup](ctx, s.pool, `SELECT id, name, description, lcr_mode, created_at, updated_at FROM route_groups WHERE id = $1 AND deleted_at IS NULL`, id)
}

// UpsertRouteGroup inserts or updates by name.
func (s *Store) UpsertRouteGroup(ctx context.Context, name, description string) (*model.RouteGroup, error) {
	return one[model.RouteGroup](ctx, s.pool, `
		INSERT INTO route_groups (name, description) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description, deleted_at = NULL
		RETURNING id, name, description, lcr_mode, created_at, updated_at`, name, description)
}

// RoutesForGroup returns the enabled routes of a group with their ordered,
// enabled carriers (the input of the route trie).
func (s *Store) RoutesForGroup(ctx context.Context, groupID uuid.UUID) ([]model.Route, error) {
	routes, err := many[model.Route](ctx, s.pool, `SELECT r.id, r.route_group_id, r.prefix, r.destination, r.enabled, g.lcr_mode, r.created_at, r.updated_at
		FROM routes r JOIN route_groups g ON g.id = r.route_group_id WHERE r.route_group_id = $1 AND r.enabled ORDER BY r.prefix`, groupID)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return routes, nil
	}
	rcs, err := many[model.RouteCarrier](ctx, s.pool, `
		SELECT rc.id, rc.route_id, rc.carrier_id, rc.priority, rc.weight, rc.enabled, rc."window"
		FROM route_carriers rc
		JOIN routes r ON r.id = rc.route_id
		JOIN carriers c ON c.id = rc.carrier_id AND c.deleted_at IS NULL AND c.status = 'active'
		WHERE r.route_group_id = $1 AND rc.enabled
		ORDER BY rc.route_id, rc.priority, rc.weight DESC`, groupID)
	if err != nil {
		return nil, err
	}
	byRoute := map[uuid.UUID][]model.RouteCarrier{}
	for _, rc := range rcs {
		byRoute[rc.RouteID] = append(byRoute[rc.RouteID], rc)
	}
	for i := range routes {
		routes[i].Carriers = byRoute[routes[i].ID]
	}
	return routes, nil
}

// UpsertRoute inserts or updates a route by (group, prefix).
func (s *Store) UpsertRoute(ctx context.Context, groupID uuid.UUID, prefix, destination string) (*model.Route, error) {
	return one[model.Route](ctx, s.pool, `
		INSERT INTO routes (route_group_id, prefix, destination) VALUES ($1, $2, $3)
		ON CONFLICT (route_group_id, prefix) DO UPDATE SET destination = EXCLUDED.destination, enabled = true
		RETURNING id, route_group_id, prefix, destination, enabled, created_at, updated_at`, groupID, prefix, destination)
}

// SetRouteCarriers replaces the ordered carrier list of a route.
func (s *Store) SetRouteCarriers(ctx context.Context, routeID uuid.UUID, carrierIDs []uuid.UUID) error {
	return s.WithTx(ctx, func(tx Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM route_carriers WHERE route_id = $1`, routeID); err != nil {
			return wrapErr(err)
		}
		for i, cid := range carrierIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO route_carriers (route_id, carrier_id, priority) VALUES ($1, $2, $3)`, routeID, cid, i+1); err != nil {
				return wrapErr(err)
			}
		}
		return nil
	})
}
