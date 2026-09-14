package store

import (
	"fmt"
	"strings"
)

// Page describes pagination and sorting of a list query.
type Page struct {
	Page    int
	PerPage int
	Sort    string // column name from the allowed list
	Desc    bool
}

// Normalize clamps the page parameters.
func (p Page) Normalize() Page {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 {
		p.PerPage = 50
	}
	if p.PerPage > 1000 {
		p.PerPage = 1000
	}
	return p
}

// Offset returns the SQL offset.
func (p Page) Offset() int { return (p.Page - 1) * p.PerPage }

// orderBy builds an ORDER BY clause from an allow-list of columns.
func (p Page) orderBy(allowed map[string]string, def string) string {
	col := def
	if c, ok := allowed[p.Sort]; ok {
		col = c
	}
	dir := "ASC"
	if p.Desc {
		dir = "DESC"
	}
	return fmt.Sprintf(" ORDER BY %s %s", col, dir)
}

// Listing is a page of results with the total count.
type Listing[T any] struct {
	Items   []T   `json:"items"`
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
}

// where joins conditions with AND.
func where(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}
