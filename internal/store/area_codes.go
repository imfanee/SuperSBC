package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/model"
)

const areaCols = `id, country_code, area_code, prefix, country, description, operator, kind, enabled, notes, created_at, updated_at`

// AreaCodeFilter narrows the area code list.
type AreaCodeFilter struct {
	Search      string // matches prefix, description, operator or country
	CountryCode string
	Kind        string
}

// AreaCodes lists area codes, shortest prefix first within a country.
func (s *Store) AreaCodes(ctx context.Context, f AreaCodeFilter, p Page) (*Listing[model.AreaCode], error) {
	p = p.Normalize()
	var conds []string
	var args []any
	if f.Search != "" {
		args = append(args, f.Search+"%", "%"+strings.ToLower(f.Search)+"%")
		conds = append(conds, fmt.Sprintf("(prefix LIKE $%d OR lower(description) LIKE $%d OR lower(operator) LIKE $%d OR lower(country) LIKE $%d)",
			len(args)-1, len(args), len(args), len(args)))
	}
	if f.CountryCode != "" {
		args = append(args, f.CountryCode)
		conds = append(conds, fmt.Sprintf("country_code = $%d", len(args)))
	}
	if f.Kind != "" {
		args = append(args, f.Kind)
		conds = append(conds, fmt.Sprintf("kind = $%d", len(args)))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM area_codes"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"prefix": "prefix", "description": "description", "operator": "operator", "country": "country"}, "country_code, length(prefix), prefix")
	args = append(args, p.PerPage, p.Offset())
	items, err := many[model.AreaCode](ctx, s.pool, `SELECT `+areaCols+` FROM area_codes`+where(conds)+order+
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []model.AreaCode{}
	}
	return &Listing[model.AreaCode]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// AllAreaCodes returns every enabled area code (for the matching cache).
func (s *Store) AllAreaCodes(ctx context.Context) ([]model.AreaCode, error) {
	return many[model.AreaCode](ctx, s.pool, `SELECT `+areaCols+` FROM area_codes WHERE enabled ORDER BY length(prefix) DESC`)
}

// UpsertAreaCode inserts or updates one entry by country and area code.
func (s *Store) UpsertAreaCode(ctx context.Context, a *model.AreaCode) (*model.AreaCode, error) {
	return one[model.AreaCode](ctx, s.pool, `
		INSERT INTO area_codes (country_code, area_code, country, description, operator, kind, enabled, notes)
		VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'mobile'), $7, $8)
		ON CONFLICT (country_code, area_code) DO UPDATE SET country = EXCLUDED.country, description = EXCLUDED.description,
		  operator = EXCLUDED.operator, kind = EXCLUDED.kind, enabled = EXCLUDED.enabled, notes = EXCLUDED.notes
		RETURNING `+areaCols,
		a.CountryCode, a.AreaCode, a.Country, a.Description, a.Operator, a.Kind, a.Enabled, a.Notes)
}

// UpdateAreaCode updates one entry by id.
func (s *Store) UpdateAreaCode(ctx context.Context, id uuid.UUID, a *model.AreaCode) (*model.AreaCode, error) {
	return one[model.AreaCode](ctx, s.pool, `
		UPDATE area_codes SET country_code = $2, area_code = $3, country = $4, description = $5, operator = $6,
		  kind = COALESCE(NULLIF($7, ''), 'mobile'), enabled = $8, notes = $9 WHERE id = $1 RETURNING `+areaCols,
		id, a.CountryCode, a.AreaCode, a.Country, a.Description, a.Operator, a.Kind, a.Enabled, a.Notes)
}

// DeleteAreaCode removes one entry.
func (s *Store) DeleteAreaCode(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM area_codes WHERE id = $1`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BackfillCDRAreaCodes fills in area and operator on CDRs that have none (or
// on every CDR of a period when force is set), longest prefix first. It
// returns how many rows changed.
func (s *Store) BackfillCDRAreaCodes(ctx context.Context, all bool) (int64, error) {
	cond := "d.area_prefix IS NULL"
	if all {
		cond = "true"
	}
	// Postgres refuses a LATERAL that references the row being updated, so the
	// longest match is expressed as a join plus a length filter.
	tag, err := s.pool.Exec(ctx, `
		UPDATE cdrs d SET area_prefix = a.prefix, area_description = a.description, operator_name = a.operator
		FROM area_codes a
		WHERE a.enabled AND d.called_number <> '' AND d.called_number LIKE a.prefix || '%'
		  AND length(a.prefix) = (SELECT max(length(x.prefix)) FROM area_codes x
		                          WHERE x.enabled AND d.called_number LIKE x.prefix || '%')
		  AND `+cond)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}
