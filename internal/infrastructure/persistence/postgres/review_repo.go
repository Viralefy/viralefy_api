package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type ReviewRepo struct{ db *DB }

func NewReviewRepo(db *DB) *ReviewRepo { return &ReviewRepo{db: db} }

const reviewCols = `id, user_id, order_id, plan_id, plan_category, country_code,
	rating, title, body, visible, created_at, updated_at`

func (r *ReviewRepo) Create(ctx context.Context, rev domain.Review) error {
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO reviews (id, user_id, order_id, plan_id, plan_category, country_code,
			rating, title, body, visible)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		rev.ID, rev.UserID, rev.OrderID, rev.PlanID, rev.PlanCategory, rev.CountryCode,
		rev.Rating, rev.Title, rev.Body, rev.Visible)
	return err
}

func (r *ReviewRepo) GetByOrderID(ctx context.Context, orderID string) (*domain.Review, error) {
	row := r.db.pool.QueryRow(ctx, `SELECT `+reviewCols+`
		FROM reviews WHERE order_id=$1`, orderID)
	var rev domain.Review
	err := row.Scan(&rev.ID, &rev.UserID, &rev.OrderID, &rev.PlanID, &rev.PlanCategory,
		&rev.CountryCode, &rev.Rating, &rev.Title, &rev.Body, &rev.Visible,
		&rev.CreatedAt, &rev.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// publicReviewQuery emite o shape do PublicReview com hidratação do nome do
// cliente: primeiro nome + inicial do sobrenome ("John D."). Quando o user
// não tem name, cai em "Customer".
const publicReviewQuery = `
	SELECT r.rating, r.title, r.body, r.created_at,
		COALESCE(
			NULLIF(SPLIT_PART(u.name, ' ', 1), '') ||
				CASE
					WHEN POSITION(' ' IN u.name) > 0
					THEN ' ' || LEFT(SPLIT_PART(u.name, ' ', -1), 1) || '.'
					ELSE ''
				END,
			'Customer'
		) AS author_name
	FROM reviews r
	LEFT JOIN users u ON u.id = r.user_id`

func scanPublicReviews(rows pgx.Rows) ([]domain.PublicReview, error) {
	list := []domain.PublicReview{}
	for rows.Next() {
		var p domain.PublicReview
		if err := rows.Scan(&p.Rating, &p.Title, &p.Body, &p.CreatedAt, &p.AuthorName); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

func (r *ReviewRepo) ListPublicByPlan(ctx context.Context, planID string, limit int) ([]domain.PublicReview, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.pool.Query(ctx, publicReviewQuery+`
		WHERE r.plan_id=$1 AND r.visible = TRUE
		ORDER BY r.created_at DESC
		LIMIT $2`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPublicReviews(rows)
}

func (r *ReviewRepo) ListPublicByCategory(ctx context.Context, category string, limit int) ([]domain.PublicReview, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.pool.Query(ctx, publicReviewQuery+`
		WHERE r.plan_category=$1 AND r.visible = TRUE
		ORDER BY r.created_at DESC
		LIMIT $2`, category, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPublicReviews(rows)
}

func (r *ReviewRepo) AggregateByPlan(ctx context.Context, planID string) (*domain.AggregateRating, error) {
	return r.aggregate(ctx, `WHERE plan_id=$1 AND visible = TRUE`, planID)
}

func (r *ReviewRepo) AggregateByCategory(ctx context.Context, category string) (*domain.AggregateRating, error) {
	return r.aggregate(ctx, `WHERE plan_category=$1 AND visible = TRUE`, category)
}

func (r *ReviewRepo) aggregate(ctx context.Context, where string, arg string) (*domain.AggregateRating, error) {
	row := r.db.pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(ROUND(AVG(rating)::numeric, 2), 0) FROM reviews `+where, arg)
	var count int
	var avg float64
	if err := row.Scan(&count, &avg); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	return &domain.AggregateRating{
		RatingValue: avg,
		ReviewCount: count,
		BestRating:  5,
		WorstRating: 1,
	}, nil
}

func (r *ReviewRepo) SetVisibility(ctx context.Context, id string, visible bool) error {
	tag, err := r.db.pool.Exec(ctx, `UPDATE reviews SET visible=$2, updated_at=NOW() WHERE id=$1`, id, visible)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// --- ReviewRequestRepository ---

// ReviewRequestRepo agrupa as queries que o cron de envio precisa. Encosta no
// orders + users + plans pra montar o candidate em um round-trip.
type ReviewRequestRepo struct{ db *DB }

func NewReviewRequestRepo(db *DB) *ReviewRequestRepo { return &ReviewRequestRepo{db: db} }

func (r *ReviewRequestRepo) ListReadyForReviewRequest(ctx context.Context, olderThan time.Time, limit int) ([]domain.ReviewRequestCandidate, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.pool.Query(ctx, `
		SELECT o.id, o.user_id, COALESCE(u.name,''), u.email, COALESCE(p.name,'')
		FROM orders o
		LEFT JOIN users u ON u.id = o.user_id
		LEFT JOIN plans p ON p.id = o.plan_id
		WHERE o.status = 'paid'
		  AND o.review_email_sent_at IS NULL
		  AND o.updated_at < $1
		  AND NOT EXISTS (SELECT 1 FROM reviews rv WHERE rv.order_id = o.id)
		  AND u.email IS NOT NULL AND u.email <> ''
		ORDER BY o.updated_at ASC
		LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReviewRequestCandidate{}
	for rows.Next() {
		var c domain.ReviewRequestCandidate
		if err := rows.Scan(&c.OrderID, &c.UserID, &c.UserName, &c.UserEmail, &c.PlanName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *ReviewRequestRepo) MarkReviewEmailSent(ctx context.Context, orderID string) error {
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE orders SET review_email_sent_at=NOW(), updated_at=NOW() WHERE id=$1`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
