package experiences

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"strings"
)

type Repository struct{ db *database.Postgres }

func NewRepository(db *database.Postgres) *Repository { return &Repository{db: db} }

func (r *Repository) Get(ctx context.Context, idOrSlug string) (Experience, error) {
	var e Experience
	err := r.db.Pool.QueryRow(ctx, `SELECT e.id::text,e.slug,e.title,e.short_description,e.description,e.category,e.city,e.state,COALESCE(e.village,''),e.price_per_person_paise,e.duration_minutes,e.min_group_size,e.max_group_size,e.status,e.is_featured,e.host_id::text,e.created_at::text,e.updated_at::text FROM experiences e WHERE e.status='published' AND (e.id::text=$1 OR e.slug=$1)`, idOrSlug).Scan(&e.ID, &e.Slug, &e.Title, &e.ShortDescription, &e.Description, &e.Category, &e.City, &e.State, &e.Village, &e.PricePerPersonPaise, &e.DurationMinutes, &e.MinGroupSize, &e.MaxGroupSize, &e.Status, &e.IsFeatured, &e.HostID, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}
func (r *Repository) List(ctx context.Context, q SearchQuery) ([]Experience, int, error) {
	where := []string{"e.status = 'published'"}
	args := []any{}
	n := 1
	if q.Q != "" {
		where = append(where, fmt.Sprintf("(e.title ILIKE $%d OR e.description ILIKE $%d OR e.city ILIKE $%d)", n, n, n))
		args = append(args, "%"+q.Q+"%")
		n++
	}
	if q.Category != "" {
		where = append(where, fmt.Sprintf("e.category = $%d", n))
		args = append(args, q.Category)
		n++
	}
	if q.City != "" {
		where = append(where, fmt.Sprintf("e.city ILIKE $%d", n))
		args = append(args, q.City)
		n++
	}
	if q.State != "" {
		where = append(where, fmt.Sprintf("e.state ILIKE $%d", n))
		args = append(args, q.State)
		n++
	}
	if q.Featured {
		where = append(where, "e.is_featured = true")
	}
	args = append(args, q.Limit, (q.Page-1)*q.Limit)
	query := fmt.Sprintf(`SELECT e.id::text,e.slug,e.title,e.short_description,e.description,e.category,e.city,e.state,COALESCE(e.village,''),e.price_per_person_paise,e.duration_minutes,e.min_group_size,e.max_group_size,e.status,e.is_featured,e.host_id::text,e.created_at::text,e.updated_at::text FROM experiences e WHERE %s ORDER BY e.is_featured DESC,e.updated_at DESC LIMIT $%d OFFSET $%d`, strings.Join(where, " AND "), n, n+1)
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Experience{}
	for rows.Next() {
		var e Experience
		if err := rows.Scan(&e.ID, &e.Slug, &e.Title, &e.ShortDescription, &e.Description, &e.Category, &e.City, &e.State, &e.Village, &e.PricePerPersonPaise, &e.DurationMinutes, &e.MinGroupSize, &e.MaxGroupSize, &e.Status, &e.IsFeatured, &e.HostID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	if !q.IncludeTotal {
		return items, 0, rows.Err()
	}
	var total int
	countQuery := fmt.Sprintf("SELECT count(*) FROM experiences e WHERE %s", strings.Join(where[:len(where)], " AND "))
	countArgs := args[:len(args)-2]
	if err := r.db.Pool.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil && err != pgx.ErrNoRows {
		return nil, 0, err
	}
	return items, total, rows.Err()
}
